package transfer

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func newTestServer(t *testing.T, fs *memFS) (*httptest.Server, *Manager) {
	t.Helper()
	m, _ := newTestManager(t, fs, t.TempDir())
	h := NewHandler(m)
	mux := http.NewServeMux()
	h.Register(mux, func(next http.Handler) http.Handler { return next })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, m
}

func doJSON(t *testing.T, method, url string, payload any) (*http.Response, map[string]any) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		raw, _ := json.Marshal(payload)
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	return resp, out
}

func TestHTTPTransferAssetScope(t *testing.T) {
	fs := newMemFS()
	m, _ := newTestManager(t, fs, t.TempDir())
	allow := func(_ *http.Request, assetID string) bool { return assetID == "asset-1" }
	h := NewHandler(m, allow, allow)
	mux := http.NewServeMux()
	h.Register(mux, func(next http.Handler) http.Handler { return next })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, _ := doJSON(t, http.MethodPost, srv.URL+"/api/v1/transfers", map[string]any{
		"asset_id": "asset-2", "direction": "upload", "remote_path": "/tmp/blocked.bin", "size": 4,
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("create outside scope=%d", resp.StatusCode)
	}
	resp, created := doJSON(t, http.MethodPost, srv.URL+"/api/v1/transfers", map[string]any{
		"asset_id": "asset-1", "direction": "upload", "remote_path": "/tmp/allowed.bin", "size": 4,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create inside scope=%d %v", resp.StatusCode, created)
	}
	var list struct {
		Items []Task `json:"items"`
		Count int    `json:"count"`
	}
	code := getJSONTransfer(t, srv.URL+"/api/v1/transfers", &list)
	if code != 200 || list.Count != 1 || len(list.Items) != 1 || list.Items[0].AssetID != "asset-1" {
		t.Fatalf("transfer list scope leaked: %d %+v", code, list)
	}
	if code := getJSONTransfer(t, srv.URL+"/api/v1/transfers?asset_id=asset-2", nil); code != 404 {
		t.Fatalf("list outside scope=%d", code)
	}
}

func getJSONTransfer(t *testing.T, url string, out any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return resp.StatusCode
}

func TestHTTPUploadLifecycle(t *testing.T) {
	fs := newMemFS()
	srv, _ := newTestServer(t, fs)
	payload := []byte("http transfer payload")

	resp, created := doJSON(t, http.MethodPost, srv.URL+"/api/v1/transfers", map[string]any{
		"asset_id": "asset-1", "direction": "upload", "remote_path": "/tmp/h.bin", "size": len(payload),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d: %v", resp.StatusCode, created)
	}
	id := created["id"].(string)

	// Out-of-order chunk must be refused without touching the file.
	bad, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/transfers/"+id+"/chunk?offset=999", bytes.NewReader(payload[:4]))
	bad.Header.Set("Content-Type", "application/octet-stream")
	badResp, err := http.DefaultClient.Do(bad)
	if err != nil {
		t.Fatalf("bad chunk: %v", err)
	}
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for wrong offset, got %d", badResp.StatusCode)
	}

	resp2 := putChunk(t, srv.URL, id, 0, payload)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("chunk status %d", resp2.StatusCode)
	}
	var task map[string]any
	_ = json.NewDecoder(resp2.Body).Decode(&task)
	resp2.Body.Close()
	if task["status"] != StatusCompleted {
		t.Fatalf("expected completed, got %v", task["status"])
	}
	if string(fs.files["/tmp/h.bin"]) != string(payload) {
		t.Fatalf("remote content mismatch: %q", string(fs.files["/tmp/h.bin"]))
	}
}

func putChunk(t *testing.T, base, id string, offset int, data []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, base+"/api/v1/transfers/"+id+"/chunk?offset="+strconv.Itoa(offset), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("chunk request: %v", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chunk do: %v", err)
	}
	return resp
}

func TestHTTPDownloadRange(t *testing.T) {
	fs := newMemFS()
	content := []byte("abcdefghij")
	fs.files["/tmp/r.bin"] = content
	srv, _ := newTestServer(t, fs)

	resp, created := doJSON(t, http.MethodPost, srv.URL+"/api/v1/transfers", map[string]any{
		"asset_id": "asset-1", "direction": "download", "remote_path": "/tmp/r.bin",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d: %v", resp.StatusCode, created)
	}
	id := created["id"].(string)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/transfers/"+id+"/content", nil)
	req.Header.Set("Range", "bytes=3-5")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("content: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusPartialContent {
		t.Fatalf("expected 206, got %d", res.StatusCode)
	}
	if string(body) != "def" {
		t.Fatalf("range body %q", string(body))
	}
	if got := res.Header.Get("Content-Range"); got != "bytes 3-5/10" {
		t.Fatalf("content-range %q", got)
	}

	// A partial range must not be enough to declare the download finished.
	if resp2, _ := doJSON(t, http.MethodPost, srv.URL+"/api/v1/transfers/"+id+"/complete", nil); resp2.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for incomplete download, got %d", resp2.StatusCode)
	}
	fullReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/transfers/"+id+"/content", nil)
	fullRes, err := http.DefaultClient.Do(fullReq)
	if err != nil {
		t.Fatalf("full content: %v", err)
	}
	fullBody, _ := io.ReadAll(fullRes.Body)
	fullRes.Body.Close()
	if string(fullBody) != string(content) {
		t.Fatalf("full body %q", string(fullBody))
	}
	if resp3, _ := doJSON(t, http.MethodPost, srv.URL+"/api/v1/transfers/"+id+"/complete", nil); resp3.StatusCode != http.StatusOK {
		t.Fatalf("complete status %d", resp3.StatusCode)
	}

	badReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/transfers/"+id+"/content", nil)
	badReq.Header.Set("Range", "bytes=999-")
	badRes, err := http.DefaultClient.Do(badReq)
	if err != nil {
		t.Fatalf("bad range: %v", err)
	}
	badRes.Body.Close()
	if badRes.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("expected 416, got %d", badRes.StatusCode)
	}
}

func TestHTTPRejectsEmptyChunkAndUnknownTask(t *testing.T) {
	fs := newMemFS()
	srv, _ := newTestServer(t, fs)
	resp, task := doJSON(t, http.MethodPost, srv.URL+"/api/v1/transfers", map[string]any{
		"asset_id": "asset-1", "direction": "upload", "remote_path": "/tmp/z.bin", "size": 4,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d: %v", resp.StatusCode, task)
	}
	id := task["id"].(string)

	empty, err := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/transfers/"+id+"/chunk?offset=0", http.NoBody)
	if err != nil {
		t.Fatalf("empty chunk request: %v", err)
	}
	empty.Header.Set("Content-Type", "application/octet-stream")
	res, err := http.DefaultClient.Do(empty)
	if err != nil {
		t.Fatalf("empty chunk: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusLengthRequired {
		t.Fatalf("expected 411, got %d", res.StatusCode)
	}

	missing, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/transfers/deadbeef", nil)
	if err != nil {
		t.Fatalf("missing request: %v", err)
	}
	mres, err := http.DefaultClient.Do(missing)
	if err != nil {
		t.Fatalf("missing: %v", err)
	}
	mres.Body.Close()
	if mres.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", mres.StatusCode)
	}
}

func TestHTTPCancelRemovesStaging(t *testing.T) {
	fs := newMemFS()
	srv, m := newTestServer(t, fs)
	resp, task := doJSON(t, http.MethodPost, srv.URL+"/api/v1/transfers", map[string]any{
		"asset_id": "asset-1", "direction": "upload", "remote_path": "/tmp/c2.bin", "size": 8,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d: %v", resp.StatusCode, task)
	}
	id := task["id"].(string)
	chunkResp := putChunk(t, srv.URL, id, 0, []byte("abcd"))
	chunkResp.Body.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/transfers/"+id, nil)
	del, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	del.Body.Close()
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", del.StatusCode)
	}
	staging := stagingPath("/tmp/c2.bin", id)
	if _, ok := fs.files[staging]; ok {
		t.Fatal("staging file survived cancel")
	}
	if got, _ := m.Get(id); got.Status != StatusCanceled {
		t.Fatalf("expected canceled, got %s", got.Status)
	}
	if _, ok := fs.files["/tmp/c2.bin"]; ok {
		t.Fatal("cancel must not create the destination file")
	}
}

func TestHTTPListFiltersByAsset(t *testing.T) {
	fs := newMemFS()
	srv, _ := newTestServer(t, fs)
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/transfers", map[string]any{
		"asset_id": "asset-1", "direction": "upload", "remote_path": "/tmp/l1.bin", "size": 1,
	})
	res, err := http.Get(srv.URL + "/api/v1/transfers?asset_id=other")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer res.Body.Close()
	var body struct {
		Items []Task `json:"items"`
		Count int    `json:"count"`
	}
	_ = json.NewDecoder(res.Body).Decode(&body)
	if body.Count != 0 {
		t.Fatalf("expected no tasks for another asset, got %d", body.Count)
	}
	if !strings.Contains(res.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("unexpected content type %q", res.Header.Get("Content-Type"))
	}
}

func TestTaskStoreSurvivesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	st, err := newTaskStore(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	st2, err := newTaskStore(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	if len(st2.list("")) != 0 {
		t.Fatal("corrupt task file should be ignored, not loaded")
	}
	if _, ok := st.get("broken"); ok {
		t.Fatal("corrupt task should not be addressable")
	}
}
