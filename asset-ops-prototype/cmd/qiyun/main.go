package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"qiyun-asset-ops/internal/metrics"
	"qiyun-asset-ops/internal/secretbox"
	"qiyun-asset-ops/internal/sshgateway"
	"qiyun-asset-ops/internal/store"
	"qiyun-asset-ops/internal/transfer"
)

type app struct {
	store    *store.Store
	ssh      *sshgateway.Manager
	transfer *transfer.Manager
	metrics  *metrics.Collector
	token    string
	webRoot  string
}

type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

func main() {
	addr := flag.String("addr", env("QIYUN_ADDR", "127.0.0.1:8765"), "HTTP listen address")
	dataDir := flag.String("data", env("QIYUN_DATA_DIR", "./data"), "persistent data directory")
	webRoot := flag.String("web", env("QIYUN_WEB_ROOT", "."), "frontend directory")
	metricsInterval := flag.Duration("metrics-interval", envDuration("QIYUN_METRICS_INTERVAL", 60*time.Second), "SSH host metrics polling interval, 0 disables polling")
	flag.Parse()
	token := strings.TrimSpace(os.Getenv("QIYUN_ADMIN_TOKEN"))
	host, _, err := net.SplitHostPort(*addr)
	if err != nil {
		fatal("invalid listen address", err)
	}
	if token == "" && !isLoopbackHost(host) {
		fatal("refusing unauthenticated non-loopback listener", errors.New("set QIYUN_ADMIN_TOKEN or bind to 127.0.0.1"))
	}
	if err = os.MkdirAll(*dataDir, 0o700); err != nil {
		fatal("create data directory", err)
	}
	box, err := secretbox.LoadOrCreate(filepath.Join(*dataDir, "master.key"))
	if err != nil {
		fatal("load master key", err)
	}
	st, err := store.Open(filepath.Join(*dataDir, "qiyun.db"), box)
	if err != nil {
		fatal("open database", err)
	}
	defer st.Close()
	cidrs := splitEnv("QIYUN_ALLOWED_CIDRS")
	origins := splitEnv("QIYUN_ALLOWED_ORIGINS")
	if len(origins) == 0 {
		origins = []string{"127.0.0.1:*", "localhost:*", "[::1]:*"}
	}
	gw, err := sshgateway.New(st, cidrs, origins)
	if err != nil {
		fatal("configure SSH gateway", err)
	}
	a := &app{store: st, ssh: gw, token: token, webRoot: *webRoot}
	tm, err := transfer.New(st, transfer.SFTPFactory(gw), st, transfer.Options{DataDir: *dataDir})
	if err != nil {
		fatal("configure transfer manager", err)
	}
	a.transfer = tm
	// Share the SSH gateway so metrics reuse the same host-key verification as
	// terminals and transfers instead of trusting hosts a second way.
	mc, err := metrics.New(st, metrics.SSHDialer(gw), metrics.Options{DataDir: *dataDir, Interval: *metricsInterval})
	if err != nil {
		fatal("configure metrics collector", err)
	}
	a.metrics = mc
	mc.Start()
	defer mc.Close()
	srv := &http.Server{Addr: *addr, Handler: a.routes(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	go func() {
		slog.Info("qiyun Go service started", "address", "http://"+*addr, "data", *dataDir, "auth_enabled", token != "")
		if e := srv.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
			fatal("serve", e)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "runtime": "go", "time": time.Now().UTC()})
	})
	mux.Handle("GET /api/v1/assets", a.auth(http.HandlerFunc(a.listAssets)))
	mux.Handle("POST /api/v1/assets", a.auth(http.HandlerFunc(a.createAsset)))
	mux.Handle("GET /api/v1/assets/{id}", a.auth(http.HandlerFunc(a.getAsset)))
	mux.Handle("PATCH /api/v1/assets/{id}", a.auth(http.HandlerFunc(a.updateAsset)))
	mux.Handle("DELETE /api/v1/assets/{id}", a.auth(http.HandlerFunc(a.deleteAsset)))
	mux.Handle("POST /api/v1/assets/{id}/host-key/scan", a.auth(http.HandlerFunc(a.scanHostKey)))
	mux.Handle("POST /api/v1/assets/{id}/host-key/approve", a.auth(http.HandlerFunc(a.approveHostKey)))
	mux.Handle("POST /api/v1/assets/{id}/test", a.auth(http.HandlerFunc(a.testAsset)))
	mux.Handle("POST /api/v1/assets/{id}/diagnose", a.auth(http.HandlerFunc(a.diagnoseAsset)))
	mux.Handle("PUT /api/v1/assets/{id}/credential", a.auth(http.HandlerFunc(a.updateCredential)))
	mux.Handle("POST /api/v1/terminal-sessions", a.auth(http.HandlerFunc(a.createTerminalSession)))
	mux.Handle("GET /api/v1/terminal-sessions/{id}/ws", http.HandlerFunc(a.ssh.ServeWS))
	transfer.NewHandler(a.transfer).Register(mux, a.auth)
	metrics.NewHandler(a.metrics).Register(mux, a.auth)
	mux.Handle("/", securityHeaders(staticHandler(a.webRoot)))
	return requestLog(mux)
}

// staticHandler serves only what the frontend needs. Serving the whole working
// directory (the default `-web .`) exposed data/master.key, the SQLite database
// and the Go sources over HTTP; with the master key an attacker can decrypt every
// stored SSH credential, so the allowlist is deliberately a hard 404 for anything
// unrecognised instead of a blocklist of known-dangerous names.
func staticHandler(root string) http.Handler {
	files := http.FileServer(http.Dir(root))
	// Subdirectories that are served wholesale.
	openDirs := map[string]bool{"vendor": true, "assets": true}
	// Extensions the pages actually load.
	allowedExt := map[string]bool{
		".html": true, ".js": true, ".css": true, ".svg": true,
		".png": true, ".jpg": true, ".jpeg": true, ".ico": true,
		".woff": true, ".woff2": true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested := path.Clean("/" + r.URL.Path)
		segments := strings.Split(strings.TrimPrefix(requested, "/"), "/")
		if len(segments) > 1 && !openDirs[segments[0]] {
			http.NotFound(w, r)
			return
		}
		if requested != "/" && !allowedExt[strings.ToLower(path.Ext(requested))] {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}

func (a *app) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.token == "" {
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" {
			got = r.URL.Query().Get("access_token")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(a.token)) != 1 {
			writeProblem(w, 401, "UNAUTHORIZED", "需要有效的管理令牌")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *app) listAssets(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	items, err := a.store.SearchAssets(r.Context(), store.AssetFilter{
		Query:       query.Get("q"),
		Department:  query.Get("department"),
		Environment: query.Get("environment"),
		HostKey:     query.Get("host_key_status"),
		Status:      query.Get("status"),
	})
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "count": len(items)})
}
func (a *app) getAsset(w http.ResponseWriter, r *http.Request) {
	item, err := a.store.GetAsset(r.Context(), r.PathValue("id"))
	if err != nil {
		notFoundOrServer(w, err)
		return
	}
	writeJSON(w, 200, item)
}
func (a *app) updateAsset(w http.ResponseWriter, r *http.Request) {
	var patch store.AssetPatch
	if !decodeJSON(w, r, &patch) {
		return
	}
	item, err := a.store.UpdateAsset(r.Context(), r.PathValue("id"), patch)
	if err != nil {
		if store.IsNotFound(err) {
			writeProblem(w, 404, "NOT_FOUND", "资产不存在或已删除")
			return
		}
		writeProblem(w, 400, "INVALID_ASSET", err.Error())
		return
	}
	writeJSON(w, 200, item)
}
func (a *app) createAsset(w http.ResponseWriter, r *http.Request) {
	var in store.CreateAsset
	if !decodeJSON(w, r, &in) {
		return
	}
	item, err := a.store.CreateAsset(r.Context(), in)
	if err != nil {
		if store.IsUniqueViolation(err) {
			writeProblem(w, 409, "ASSET_CODE_CONFLICT", "资产编码已被一个未删除的资产占用，请换一个编码，或先删除占用该编码的资产")
			return
		}
		writeProblem(w, 400, "INVALID_ASSET", err.Error())
		return
	}
	w.Header().Set("Location", "/api/v1/assets/"+item.ID)
	writeJSON(w, 201, item)
}
func (a *app) deleteAsset(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteAsset(r.Context(), r.PathValue("id")); err != nil {
		notFoundOrServer(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a *app) scanHostKey(w http.ResponseWriter, r *http.Request) {
	asset, err := a.store.GetAsset(r.Context(), r.PathValue("id"))
	if err != nil {
		notFoundOrServer(w, err)
		return
	}
	key, fp, target, err := a.ssh.ScanHostKey(r.Context(), asset)
	if err != nil {
		stage, hint := "", ""
		var connErr *sshgateway.ConnError
		if errors.As(err, &connErr) {
			stage, hint = connErr.Stage, connErr.Hint
		}
		slog.Warn("host key scan failed", "asset_id", asset.ID, "address", asset.Address, "port", asset.SSHPort, "stage", stage, "error", err)
		a.store.Audit(r.Context(), "asset.host_key.scan", "asset", asset.ID, "failed", map[string]any{"stage": stage, "error": err.Error()})
		w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "about:blank", "title": http.StatusText(http.StatusBadGateway), "status": http.StatusBadGateway,
			"code": "SSH_HOST_KEY_SCAN_FAILED", "detail": err.Error(), "stage": stage, "hint": hint,
		})
		return
	}
	a.store.Audit(r.Context(), "asset.host_key.scan", "asset", asset.ID, "success", map[string]any{"fingerprint": fp, "target": target})
	writeJSON(w, 200, map[string]any{"asset_id": asset.ID, "host_key": key, "fingerprint": fp, "target": target})
}
func (a *app) approveHostKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		HostKey     string `json:"host_key"`
		Fingerprint string `json:"fingerprint"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	asset, err := a.store.GetAsset(r.Context(), r.PathValue("id"))
	if err != nil {
		notFoundOrServer(w, err)
		return
	}
	currentKey, currentFingerprint, _, err := a.ssh.ScanHostKey(r.Context(), asset)
	if err != nil {
		writeProblem(w, 502, "SSH_HOST_KEY_SCAN_FAILED", err.Error())
		return
	}
	if body.HostKey != currentKey || body.Fingerprint != currentFingerprint {
		writeProblem(w, 409, "HOST_KEY_CHANGED", "SSH 主机公钥与刚才扫描结果不一致，请重新核验")
		return
	}
	if err := a.store.ApproveHostKey(r.Context(), asset.ID, currentKey, currentFingerprint); err != nil {
		notFoundOrServer(w, err)
		return
	}
	item, _ := a.store.GetAsset(r.Context(), asset.ID)
	writeJSON(w, 200, item)
}
func (a *app) testAsset(w http.ResponseWriter, r *http.Request) {
	d, err := a.ssh.Test(r.Context(), r.PathValue("id"))
	if err != nil {
		if d.AssetID == "" {
			notFoundOrServer(w, err)
			return
		}
		a.store.Audit(r.Context(), "asset.ssh.test", "asset", r.PathValue("id"), "failed", map[string]any{"summary": d.Summary})
		writeJSON(w, 200, map[string]any{"status": "failed", "diagnostic": d})
		return
	}
	a.store.Audit(r.Context(), "asset.ssh.test", "asset", r.PathValue("id"), "success", nil)
	writeJSON(w, 200, map[string]any{"status": "connected", "diagnostic": d})
}
func (a *app) diagnoseAsset(w http.ResponseWriter, r *http.Request) {
	d, err := a.ssh.Diagnose(r.Context(), r.PathValue("id"))
	if err != nil {
		notFoundOrServer(w, err)
		return
	}
	result := "failed"
	if d.OK {
		result = "success"
	}
	a.store.Audit(r.Context(), "asset.ssh.diagnose", "asset", r.PathValue("id"), result, map[string]any{"summary": d.Summary, "target": d.Target, "key_fingerprint": d.KeyFingerprint})
	writeJSON(w, 200, d)
}
func (a *app) updateCredential(w http.ResponseWriter, r *http.Request) {
	var in store.CreateAsset
	if !decodeJSON(w, r, &in) {
		return
	}
	item, err := a.store.UpdateCredential(r.Context(), r.PathValue("id"), in)
	if err != nil {
		if store.IsNotFound(err) {
			writeProblem(w, 404, "NOT_FOUND", "资源不存在")
			return
		}
		writeProblem(w, 400, "INVALID_CREDENTIAL", err.Error())
		return
	}
	writeJSON(w, 200, item)
}
func (a *app) createTerminalSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AssetID string `json:"asset_id"`
		// The browser sends the initial terminal size; it must be accepted here or
		// the strict JSON decoder rejects the whole request with 400.
		Cols int    `json:"cols"`
		Rows int    `json:"rows"`
		Term string `json:"term"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	t, err := a.ssh.CreateTicketWithSize(body.AssetID, body.Cols, body.Rows)
	if err != nil {
		if strings.Contains(err.Error(), "HOST_KEY_UNKNOWN") {
			writeProblem(w, 409, "HOST_KEY_UNKNOWN", "请先扫描并批准 SSH 主机指纹")
			return
		}
		notFoundOrServer(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": t.SessionID, "asset_id": t.AssetID, "ws_url": "/api/v1/terminal-sessions/" + t.SessionID + "/ws", "ws_ticket": t.Value, "expires_at": t.Expires})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		writeProblem(w, 400, "INVALID_JSON", err.Error())
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{Type: "about:blank", Title: http.StatusText(status), Status: status, Code: code, Detail: detail})
}
func serverError(w http.ResponseWriter, err error) {
	slog.Error("request failed", "error", err)
	writeProblem(w, 500, "INTERNAL_ERROR", "服务端处理失败")
}
func notFoundOrServer(w http.ResponseWriter, err error) {
	if err != nil && (strings.Contains(strings.ToLower(err.Error()), "no rows") || strings.Contains(strings.ToLower(err.Error()), "not found")) {
		writeProblem(w, 404, "NOT_FOUND", "资源不存在")
		return
	}
	serverError(w, err)
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self' ws: wss:")
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the response code so logs show failures instead of just
// the path. Without it a 400/401 looked identical to a success.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) { s.status = code; s.ResponseWriter.WriteHeader(code) }
func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Unwrap keeps http.ResponseController (WebSocket hijack) working through the wrapper.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			status := recorder.status
			if status == 0 {
				status = http.StatusOK
			}
			level := slog.LevelInfo
			if status >= 400 {
				level = slog.LevelWarn
			}
			slog.Log(r.Context(), level, "http request", "method", r.Method, "path", r.URL.Path, "status", status, "duration_ms", time.Since(start).Milliseconds())
		}
	})
}
func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
func envDuration(k string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(k))
	if raw == "" {
		return def
	}
	if raw == "off" || raw == "0" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return def
	}
	return d
}

func splitEnv(k string) []string {
	raw := strings.TrimSpace(os.Getenv(k))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := parts[:0]
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}
func isLoopbackHost(h string) bool {
	if h == "localhost" || h == "" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
func fatal(msg string, err error) { fmt.Fprintln(os.Stderr, msg+":", err); os.Exit(1) }
