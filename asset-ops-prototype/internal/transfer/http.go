package transfer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type Handler struct {
	m *Manager
}

func NewHandler(m *Manager) *Handler { return &Handler{m: m} }

func (h *Handler) Register(mux *http.ServeMux, auth func(http.Handler) http.Handler) {
	mux.Handle("GET /api/v1/transfers", auth(http.HandlerFunc(h.list)))
	mux.Handle("POST /api/v1/transfers", auth(http.HandlerFunc(h.create)))
	mux.Handle("GET /api/v1/transfers/{id}", auth(http.HandlerFunc(h.get)))
	mux.Handle("PUT /api/v1/transfers/{id}/chunk", auth(http.HandlerFunc(h.chunk)))
	mux.Handle("POST /api/v1/transfers/{id}/complete", auth(http.HandlerFunc(h.complete)))
	mux.Handle("GET /api/v1/transfers/{id}/content", auth(http.HandlerFunc(h.content)))
	mux.Handle("HEAD /api/v1/transfers/{id}/content", auth(http.HandlerFunc(h.content)))
	mux.Handle("DELETE /api/v1/transfers/{id}", auth(http.HandlerFunc(h.remove)))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	items := h.m.List(r.URL.Query().Get("asset_id"))
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AssetID    string `json:"asset_id"`
		Direction  string `json:"direction"`
		RemotePath string `json:"remote_path"`
		FileName   string `json:"file_name"`
		Size       int64  `json:"size"`
		Overwrite  bool   `json:"overwrite"`
	}
	if !decode(w, r, &body, 64<<10) {
		return
	}
	task, err := h.m.Create(r.Context(), CreateRequest{
		AssetID: body.AssetID, Direction: body.Direction, RemotePath: body.RemotePath,
		FileName: body.FileName, Size: body.Size, Overwrite: body.Overwrite,
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, task)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	task, err := h.m.Get(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

// chunk accepts a raw request body so the browser can stream a file slice
// without base64 or multipart overhead.
func (h *Handler) chunk(w http.ResponseWriter, r *http.Request) {
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset < 0 {
		writeProblem(w, http.StatusBadRequest, "INVALID_OFFSET", "offset 必须是非负整数")
		return
	}
	if r.ContentLength <= 0 {
		writeProblem(w, http.StatusLengthRequired, "LENGTH_REQUIRED", "分块请求必须携带正的 Content-Length")
		return
	}
	if r.ContentLength > MaxChunk {
		writeProblem(w, http.StatusRequestEntityTooLarge, "CHUNK_TOO_LARGE", fmt.Sprintf("单个分块不能超过 %d 字节", MaxChunk))
		return
	}
	body := http.MaxBytesReader(w, r.Body, MaxChunk)
	task, err := h.m.WriteChunk(r.Context(), r.PathValue("id"), offset, body, r.ContentLength)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h *Handler) complete(w http.ResponseWriter, r *http.Request) {
	task, err := h.m.Complete(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := h.m.Get(id)
	if err != nil {
		fail(w, err)
		return
	}
	if task.Status == StatusCompleted || task.Status == StatusCanceled || task.Status == StatusFailed {
		if err := h.m.Forget(id); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, err := h.m.Cancel(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// content streams the remote file. Range support is what makes an interrupted
// download resumable: the client re-requests from its last confirmed offset.
func (h *Handler) content(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := h.m.Get(id)
	if err != nil {
		fail(w, err)
		return
	}
	if task.Direction != DirectionDownload {
		writeProblem(w, http.StatusConflict, "NOT_DOWNLOAD", "该任务不是下载任务")
		return
	}
	start, end, err := parseRange(r.Header.Get("Range"), task.Size)
	if err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", task.Size))
		writeProblem(w, http.StatusRequestedRangeNotSatisfiable, "INVALID_RANGE", err.Error())
		return
	}
	length := end - start + 1
	reader, _, err := h.m.OpenDownload(r.Context(), id, start)
	if err != nil {
		fail(w, err)
		return
	}
	defer reader.Close()

	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	filename := strings.ReplaceAll(strings.ReplaceAll(task.FileName, `"`, ""), "\r", "")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	partial := start != 0 || end != task.Size-1
	if partial {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, task.Size))
	}
	status := http.StatusOK
	if partial {
		status = http.StatusPartialContent
	}
	w.WriteHeader(status)
	// Bounded copy: memory stays flat regardless of file size. The reader is
	// capped at the negotiated range so the body never exceeds Content-Length.
	buf := make([]byte, 256<<10)
	if _, err := io.CopyBuffer(w, io.LimitReader(reader, length), buf); err != nil {
		// The header is already written, so the only correct signal left is an
		// aborted connection; the client resumes from its own confirmed offset.
		return
	}
}

func parseRange(header string, size int64) (int64, int64, error) {
	if size < 0 {
		return 0, 0, errors.New("文件大小未知")
	}
	header = strings.TrimSpace(header)
	if header == "" {
		if size == 0 {
			return 0, 0, nil
		}
		return 0, size - 1, nil
	}
	if !strings.HasPrefix(header, "bytes=") {
		return 0, 0, errors.New("仅支持 bytes 范围")
	}
	spec := strings.TrimPrefix(header, "bytes=")
	if strings.Contains(spec, ",") {
		return 0, 0, errors.New("不支持多段范围")
	}
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, errors.New("范围格式非法")
	}
	var start, end int64
	if parts[0] == "" {
		// Suffix form: last N bytes.
		n, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, errors.New("范围格式非法")
		}
		if n > size {
			n = size
		}
		start, end = size-n, size-1
	} else {
		v, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, 0, errors.New("范围起点非法")
		}
		start = v
		if parts[1] == "" {
			end = size - 1
		} else {
			e, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				return 0, 0, errors.New("范围终点非法")
			}
			end = e
		}
	}
	if start < 0 || start >= size || end < start {
		return 0, 0, errors.New("范围超出文件大小")
	}
	if end > size-1 {
		end = size - 1
	}
	return start, end, nil
}

func decode(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		writeProblem(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return false
	}
	return true
}

func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeProblem(w, http.StatusNotFound, "TRANSFER_NOT_FOUND", "传输任务不存在")
	case errors.Is(err, ErrOffset):
		writeProblem(w, http.StatusConflict, "OFFSET_MISMATCH", err.Error())
	case errors.Is(err, ErrConflict):
		writeProblem(w, http.StatusConflict, "TRANSFER_CONFLICT", err.Error())
	case errors.Is(err, ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "INVALID_TRANSFER", err.Error())
	case errors.Is(err, ErrTooLarge):
		writeProblem(w, http.StatusRequestEntityTooLarge, "TRANSFER_TOO_LARGE", err.Error())
	case errors.Is(err, ErrRemoteNotFile):
		writeProblem(w, http.StatusBadRequest, "NOT_A_FILE", "远端路径不是普通文件")
	default:
		msg := err.Error()
		if strings.Contains(msg, "no rows") || strings.Contains(msg, "not found") {
			writeProblem(w, http.StatusNotFound, "NOT_FOUND", "资源不存在")
			return
		}
		// A TCP timeout is a network/VPN problem, not an SFTP or credential one;
		// saying so saves the user from chasing the wrong cause.
		if strings.Contains(msg, "i/o timeout") || strings.Contains(msg, "no route to host") || strings.Contains(msg, "connection refused") || strings.Contains(msg, "network is unreachable") {
			// 终端连的是另一台主机也一样会看到这条提示，所以要点明这是“本次传输的目标”。
			writeProblem(w, http.StatusBadGateway, "TRANSFER_REMOTE_UNREACHABLE",
				msg+"（本次传输的目标主机连不上：请确认该地址与登记的 SSH 端口、以及 VPN/网络。终端已连上别的服务器不代表这台也可达）")
			return
		}
		// Connection, authentication and SFTP failures are upstream problems, not
		// bugs in this service.
		writeProblem(w, http.StatusBadGateway, "TRANSFER_REMOTE_FAILED", msg)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type": "about:blank", "title": http.StatusText(status), "status": status,
		"code": code, "detail": detail,
	})
}
