package metrics

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// Handler exposes the collector over HTTP.
type Handler struct {
	c *Collector
}

func NewHandler(c *Collector) *Handler { return &Handler{c: c} }

func (h *Handler) Register(mux *http.ServeMux, auth func(http.Handler) http.Handler) {
	mux.Handle("GET /api/v1/metrics", auth(http.HandlerFunc(h.list)))
	mux.Handle("POST /api/v1/metrics/{id}/collect", auth(http.HandlerFunc(h.collect)))
	mux.Handle("GET /api/v1/metrics/{id}/history", auth(http.HandlerFunc(h.history)))
}

// item is the per-host payload the dashboard renders. A host that was never
// sampled still appears with status "pending" so the row is not silently blank.
type item struct {
	AssetID         string    `json:"asset_id"`
	Status          string    `json:"status"`
	Error           string    `json:"error,omitempty"`
	CollectedAt     *time.Time `json:"collected_at,omitempty"`
	AgeSeconds      int64     `json:"age_seconds"`
	DurationMS      int64     `json:"duration_ms"`
	IntervalSeconds int       `json:"interval_seconds"`
	RetryInSeconds  int64     `json:"retry_in_seconds,omitempty"`
	Snapshot        *Snapshot `json:"snapshot,omitempty"`
}

func (h *Handler) toItem(assetID string) item {
	rec, ok := h.c.Latest(assetID)
	out := item{AssetID: assetID, Status: StatusPending, IntervalSeconds: int(h.c.Interval().Seconds())}
	if !ok {
		return out
	}
	out.Status, out.Error, out.DurationMS = rec.Status, rec.Error, rec.DurationMS
	if !rec.CollectedAt.IsZero() {
		at := rec.CollectedAt
		out.CollectedAt = &at
	}
	out.Snapshot = rec.Snapshot
	if !rec.CollectedAt.IsZero() {
		out.AgeSeconds = int64(time.Since(rec.CollectedAt).Seconds())
		if out.AgeSeconds < 0 {
			out.AgeSeconds = 0
		}
	}
	if rec.Status == StatusError && !rec.NextAttempt.IsZero() {
		if wait := int64(time.Until(rec.NextAttempt).Seconds()); wait > 0 {
			out.RetryInSeconds = wait
		}
	}
	return out
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	assets, err := h.c.assets.ListAssets(ctx, r.URL.Query().Get("q"))
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "METRICS_UNAVAILABLE", err.Error())
		return
	}
	items := make([]item, 0, len(assets))
	for _, a := range assets {
		if a.AssetType != "" && a.AssetType != "linux_host" {
			continue
		}
		items = append(items, h.toItem(a.ID))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"interval_seconds": int(h.c.Interval().Seconds()),
		"collected_at":     time.Now().UTC(),
		"items":            items,
	})
}

func (h *Handler) assetExists(ctx context.Context, id string) (bool, error) {
	assets, err := h.c.assets.ListAssets(ctx, "")
	if err != nil {
		return false, err
	}
	for _, a := range assets {
		if a.ID == id {
			return true, nil
		}
	}
	return false, nil
}

// collect forces one immediate sample. It is the "刷新" button in the UI, so a
// failure is reported as 200 with status=error: the request itself succeeded,
// the host did not.
func (h *Handler) collect(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	exists, err := h.assetExists(ctx, id)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "METRICS_UNAVAILABLE", err.Error())
		return
	}
	if !exists {
		writeProblem(w, http.StatusNotFound, "ASSET_NOT_FOUND", "资产不存在")
		return
	}
	h.c.Collect(ctx, id)
	writeJSON(w, http.StatusOK, h.toItem(id))
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	limit := 120
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 2000 {
			limit = n
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	exists, err := h.assetExists(ctx, id)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "METRICS_UNAVAILABLE", err.Error())
		return
	}
	if !exists {
		writeProblem(w, http.StatusNotFound, "ASSET_NOT_FOUND", "资产不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"asset_id": id, "samples": h.c.History(id, limit)})
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
