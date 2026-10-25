package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"qiyun-asset-ops/internal/guacamole"
	"qiyun-asset-ops/internal/metrics"
	"qiyun-asset-ops/internal/rdp"
	"qiyun-asset-ops/internal/secretbox"
	"qiyun-asset-ops/internal/sshgateway"
	"qiyun-asset-ops/internal/store"
	"qiyun-asset-ops/internal/transfer"
)

type app struct {
	store       *store.Store
	ssh         *sshgateway.Manager
	transfer    *transfer.Manager
	metrics     *metrics.Collector
	rdp         *rdp.Manager
	guac        *guacamole.Client
	token       string
	agentToken  string
	webRoot     string
	authEnabled bool
	sessionTTL  time.Duration
	limiter     *loginLimiter
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
	if err = os.MkdirAll(*dataDir, 0o700); err != nil {
		fatal("create data directory", err)
	}
	agentToken := strings.TrimSpace(os.Getenv("QIYUN_AGENT_TOKEN"))
	agentTokenPath := filepath.Join(*dataDir, "agent.token")
	if agentToken == "" {
		if raw, readErr := os.ReadFile(agentTokenPath); readErr == nil {
			agentToken = strings.TrimSpace(string(raw))
		}
	}
	if agentToken == "" {
		generated, tokenErr := newAgentToken()
		if tokenErr != nil {
			fatal("generate agent token", tokenErr)
		}
		if writeErr := os.WriteFile(agentTokenPath, []byte(generated+"\n"), 0o600); writeErr != nil {
			fatal("write agent token", writeErr)
		}
		agentToken = generated
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
	authMode := strings.ToLower(strings.TrimSpace(env("QIYUN_AUTH_MODE", "auto")))
	if authMode != "auto" && authMode != "on" && authMode != "off" {
		fatal("invalid QIYUN_AUTH_MODE", errors.New("use auto, on or off"))
	}
	userCount, err := st.CountUsers(context.Background())
	if err != nil {
		fatal("count users", err)
	}
	loopback := isLoopbackHost(host)
	authEnabled := authMode == "on" || (authMode == "auto" && (!loopback || userCount > 0))
	if authMode == "off" && !loopback {
		fatal("refusing unauthenticated non-loopback listener", errors.New("set QIYUN_AUTH_MODE=on and QIYUN_ADMIN_PASSWORD"))
	}
	if authEnabled && userCount == 0 {
		if err := bootstrapAdmin(context.Background(), st, loopback); err != nil {
			fatal("bootstrap administrator", err)
		}
		userCount = 1
	}
	if !authEnabled && !loopback {
		fatal("refusing unauthenticated non-loopback listener", errors.New("enable login with QIYUN_AUTH_MODE=on"))
	}
	cidrs := splitEnv("QIYUN_ALLOWED_CIDRS")
	origins := splitEnv("QIYUN_ALLOWED_ORIGINS")
	if len(origins) == 0 {
		origins = []string{"127.0.0.1:*", "localhost:*", "[::1]:*"}
	}
	gw, err := sshgateway.New(st, cidrs, origins)
	if err != nil {
		fatal("configure SSH gateway", err)
	}
	var guacClient *guacamole.Client
	if raw := strings.TrimSpace(os.Getenv("QIYUN_GUACAMOLE_URL")); raw != "" {
		guacClient, err = guacamole.New(raw, env("QIYUN_GUACAMOLE_USER", ""), env("QIYUN_GUACAMOLE_PASSWORD", ""), env("QIYUN_GUACAMOLE_DATA_SOURCE", ""))
		if err != nil {
			fatal("configure Guacamole gateway", err)
		}
	}
	a := &app{store: st, ssh: gw, guac: guacClient, token: token, agentToken: agentToken, webRoot: *webRoot, authEnabled: authEnabled, sessionTTL: 12 * time.Hour, limiter: newLoginLimiter()}
	rm, err := rdp.New(st, st, rdp.Options{DataDir: *dataDir})
	if err != nil {
		fatal("configure RDP launcher", err)
	}
	a.rdp = rm
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
		slog.Info("qiyun Go service started", "address", "http://"+*addr, "data", *dataDir, "auth_enabled", authEnabled, "users", userCount, "web_rdp", guacClient != nil, "agent_token_file", agentTokenPath)
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
	mux.HandleFunc("POST /api/v1/auth/login", a.authLogin)
	mux.HandleFunc("GET /api/v1/auth/me", a.authMe)
	mux.Handle("POST /api/v1/auth/logout", a.auth(http.HandlerFunc(a.authLogout)))
	mux.Handle("GET /api/v1/permissions", a.auth(http.HandlerFunc(a.listPermissions)))
	mux.Handle("GET /api/v1/audit", a.permit("audit.read", http.HandlerFunc(a.listAuditEvents)))
	mux.Handle("GET /api/v1/users", a.adminOnly(http.HandlerFunc(a.listUsers)))
	mux.Handle("POST /api/v1/users", a.adminOnly(http.HandlerFunc(a.createUser)))
	mux.Handle("PATCH /api/v1/users/{id}", a.adminOnly(http.HandlerFunc(a.updateUser)))
	mux.Handle("DELETE /api/v1/users/{id}", a.adminOnly(http.HandlerFunc(a.disableUser)))
	mux.Handle("PUT /api/v1/users/{id}/password", a.adminOnly(http.HandlerFunc(a.resetUserPassword)))
	mux.Handle("GET /api/v1/users/{id}/grants", a.adminOnly(http.HandlerFunc(a.listUserGrants)))
	mux.Handle("GET /api/v1/assets", a.permit("asset.read", http.HandlerFunc(a.listAssets)))
	mux.Handle("POST /api/v1/assets", a.adminOnly(http.HandlerFunc(a.createAsset)))
	mux.Handle("GET /api/v1/assets/{id}", a.permit("asset.read", http.HandlerFunc(a.getAsset)))
	mux.Handle("PATCH /api/v1/assets/{id}", a.permit("asset.write", http.HandlerFunc(a.updateAsset)))
	mux.Handle("DELETE /api/v1/assets/{id}", a.adminOnly(http.HandlerFunc(a.deleteAsset)))
	mux.Handle("POST /api/v1/assets/{id}/host-key/scan", a.permit("asset.write", http.HandlerFunc(a.scanHostKey)))
	mux.Handle("POST /api/v1/assets/{id}/host-key/approve", a.permit("asset.write", http.HandlerFunc(a.approveHostKey)))
	mux.Handle("POST /api/v1/assets/{id}/test", a.permit("ssh.connect", http.HandlerFunc(a.testAsset)))
	mux.Handle("POST /api/v1/assets/{id}/diagnose", a.permit("ssh.connect", http.HandlerFunc(a.diagnoseAsset)))
	mux.Handle("PUT /api/v1/assets/{id}/credential", a.permit("credential.write", http.HandlerFunc(a.updateCredential)))
	mux.Handle("GET /api/v1/assets/{id}/grants", a.adminOnly(http.HandlerFunc(a.getAssetGrants)))
	mux.Handle("PUT /api/v1/assets/{id}/grants", a.adminOnly(http.HandlerFunc(a.putAssetGrants)))
	mux.Handle("POST /api/v1/terminal-sessions", a.permit("ssh.connect", http.HandlerFunc(a.createTerminalSession)))
	mux.Handle("GET /api/v1/rdp/capabilities", a.auth(http.HandlerFunc(a.rdpCapabilities)))
	mux.Handle("GET /api/v1/rdp/connector", a.auth(http.HandlerFunc(a.rdpConnectorInfo)))
	mux.Handle("POST /api/v1/rdp/signing", a.adminOnly(http.HandlerFunc(a.rdpSetupSigning)))
	mux.Handle("POST /api/v1/assets/{id}/rdp/launch", a.permit("rdp.connect", http.HandlerFunc(a.rdpLaunch)))
	mux.Handle("POST /api/v1/assets/{id}/rdp/ticket", a.permit("rdp.connect", http.HandlerFunc(a.createRDPTicket)))
	mux.Handle("POST /api/v1/assets/{id}/web-rdp", a.permit("rdp.connect", http.HandlerFunc(a.webRDP)))
	mux.HandleFunc("POST /api/v1/rdp/tickets/redeem", a.redeemRDPTicket)
	mux.HandleFunc("POST /api/v1/agent/report", a.agentReport)
	mux.Handle("GET /api/v1/agent/hosts", a.permit("asset.read", http.HandlerFunc(a.agentHosts)))
	mux.Handle("POST /api/v1/agent/hosts", a.permit("asset.write", http.HandlerFunc(a.agentCreateHost)))
	mux.Handle("PATCH /api/v1/agent/hosts/{id}", a.permit("asset.write", http.HandlerFunc(a.agentUpdateHost)))
	mux.Handle("DELETE /api/v1/agent/hosts/{id}", a.permit("asset.write", http.HandlerFunc(a.agentDeleteHost)))
	mux.Handle("POST /api/v1/agent/hosts/bulk-delete", a.permit("asset.write", http.HandlerFunc(a.agentBulkDeleteHosts)))
	mux.Handle("POST /api/v1/agent/hosts/purge", a.permit("asset.write", http.HandlerFunc(a.agentPurgeHosts)))
	mux.Handle("GET /api/v1/agent/hosts/{id}", a.permit("asset.read", http.HandlerFunc(a.agentHost)))
	mux.Handle("GET /api/v1/agent/enrollment", a.adminOnly(http.HandlerFunc(a.agentEnrollment)))
	mux.Handle("GET /api/v1/terminal-sessions/{id}/ws", a.auth(http.HandlerFunc(a.ssh.ServeWS)))
	transfer.NewHandler(a.transfer,
		func(r *http.Request, assetID string) bool { return a.assetVisible(r, assetID) },
		func(r *http.Request, assetID string) bool { return a.assetConnectable(r, assetID, "ssh.connect") },
	).Register(mux, a.auth)
	metrics.NewHandler(a.metrics,
		func(r *http.Request, assetID string) bool { return a.assetVisible(r, assetID) },
		func(r *http.Request, assetID string) bool { return a.assetConnectable(r, assetID, "ssh.connect") },
	).Register(mux, a.auth)
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
	openDirs := map[string]bool{"vendor": true, "assets": true, "downloads": true}
	// Extensions the pages actually load. Downloads is a dedicated directory
	// for the connector package; runtime state is still never served.
	allowedExt := map[string]bool{
		".html": true, ".js": true, ".css": true, ".svg": true,
		".png": true, ".jpg": true, ".jpeg": true, ".ico": true,
		".woff": true, ".woff2": true, ".zip": true,
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
	if visible := a.visibleAssetIDs(r); visible != nil {
		filtered := make([]store.Asset, 0, len(items))
		for _, item := range items {
			if visible[item.ID] {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	writeJSON(w, 200, map[string]any{"items": items, "count": len(items)})
}
func (a *app) getAsset(w http.ResponseWriter, r *http.Request) {
	assetID := r.PathValue("id")
	if !a.assetVisible(r, assetID) {
		writeProblem(w, 404, "NOT_FOUND", "资产不存在或无权访问")
		return
	}
	item, err := a.store.GetAsset(r.Context(), assetID)
	if err != nil {
		notFoundOrServer(w, err)
		return
	}
	writeJSON(w, 200, item)
}
func (a *app) updateAsset(w http.ResponseWriter, r *http.Request) {
	assetID := r.PathValue("id")
	if !a.assetEditable(r, assetID) {
		writeProblem(w, 404, "NOT_FOUND", "资产不存在或无权修改")
		return
	}
	var patch store.AssetPatch
	if !decodeJSON(w, r, &patch) {
		return
	}
	item, err := a.store.UpdateAsset(r.Context(), assetID, patch)
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
	assetID := r.PathValue("id")
	if !a.assetEditable(r, assetID) {
		writeProblem(w, 404, "NOT_FOUND", "资产不存在或无权扫描")
		return
	}
	asset, err := a.store.GetAsset(r.Context(), assetID)
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
	assetID := r.PathValue("id")
	if !a.assetEditable(r, assetID) {
		writeProblem(w, 404, "NOT_FOUND", "资产不存在或无权核验")
		return
	}
	asset, err := a.store.GetAsset(r.Context(), assetID)
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
	assetID := r.PathValue("id")
	if !a.assetConnectable(r, assetID, "ssh.connect") {
		writeProblem(w, 404, "NOT_FOUND", "资产不存在或无权连接")
		return
	}
	d, err := a.ssh.Test(r.Context(), assetID)
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
	assetID := r.PathValue("id")
	if !a.assetConnectable(r, assetID, "ssh.connect") {
		writeProblem(w, 404, "NOT_FOUND", "资产不存在或无权连接")
		return
	}
	d, err := a.ssh.Diagnose(r.Context(), assetID)
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
	assetID := r.PathValue("id")
	if !a.assetEditable(r, assetID) {
		writeProblem(w, 404, "NOT_FOUND", "资产不存在或无权修改凭据")
		return
	}
	var in store.CreateAsset
	if !decodeJSON(w, r, &in) {
		return
	}
	item, err := a.store.UpdateCredential(r.Context(), assetID, in)
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
	if !a.assetConnectable(r, body.AssetID, "ssh.connect") {
		writeProblem(w, 404, "NOT_FOUND", "资产不存在或无权连接")
		return
	}
	// A Windows asset has no SSH service by design; explaining that here is
	// clearer than letting the WebSocket attempt an SSH handshake and fail.
	if asset, err := a.store.GetAsset(r.Context(), body.AssetID); err == nil && asset.IsWindows() {
		writeProblem(w, 409, "RDP_ASSET", "该资产是 Windows 主机，请从“主机与远程”打开 RDP 远程桌面，不支持 SSH 终端")
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

// rdpCapabilities reports whether this machine can open mstsc. A remote browser
// gets available=false instead of a button that would fail on click, because
// mstsc must open on the operator's own desktop, not on the server's.
func (a *app) rdpCapabilities(w http.ResponseWriter, r *http.Request) {
	capability := a.rdp.Capabilities(r.Context())
	if !isLoopbackRequest(r) {
		capability.Available = false
		capability.Error = "RDP 只能从运行本服务的 Windows 电脑通过 http://127.0.0.1:8765 发起"
	}
	writeJSON(w, http.StatusOK, capability)
}

// rdpSetupSigning performs the one-time publisher trust setup. It can show one
// Windows UAC prompt, so it is a separate explicit action, never part of launch.
func (a *app) rdpSetupSigning(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		writeProblem(w, http.StatusForbidden, "RDP_LOCAL_WINDOWS_ONLY", "RDP 签名配置只能在本机执行")
		return
	}
	if !isSameOriginRequest(r) {
		writeProblem(w, http.StatusForbidden, "RDP_CROSS_ORIGIN", "拒绝跨站 RDP 签名配置请求")
		return
	}
	status, err := a.rdp.SetupSigning(r.Context())
	if err != nil {
		var rdpErr *rdp.RDPError
		if errors.As(err, &rdpErr) {
			writeProblem(w, rdpErr.Status, rdpErr.Code, rdpErr.Message)
		} else {
			slog.Warn("rdp signing setup failed", "error", err)
			writeProblem(w, http.StatusBadGateway, "RDP_SIGNING_SETUP_FAILED", "签名初始化失败，请查看服务控制台中的诊断信息。")
		}
		return
	}
	if !status.Configured {
		writeProblem(w, http.StatusBadGateway, "RDP_SIGNING_SETUP_FAILED", "签名脚本执行完成，但受信任发布者仍未生效")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *app) rdpLaunch(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		writeProblem(w, http.StatusForbidden, "RDP_LOCAL_WINDOWS_ONLY", "RDP 只能在运行本服务的 Windows 电脑上发起，请通过 http://127.0.0.1:8765 访问")
		return
	}
	if !isSameOriginRequest(r) {
		writeProblem(w, http.StatusForbidden, "RDP_CROSS_ORIGIN", "拒绝跨站 RDP 启动请求")
		return
	}
	assetID := r.PathValue("id")
	if !a.assetConnectable(r, assetID, "rdp.connect") {
		writeProblem(w, 404, "NOT_FOUND", "资产不存在或无权连接")
		return
	}
	var in rdp.LaunchInput
	if !decodeJSON(w, r, &in) {
		return
	}
	result, err := a.rdp.Launch(r.Context(), assetID, in)
	if err != nil {
		var rdpErr *rdp.RDPError
		if errors.As(err, &rdpErr) {
			writeProblem(w, rdpErr.Status, rdpErr.Code, rdpErr.Message)
			return
		}
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func newAgentToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
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

// isLoopbackRequest treats only real loopback peers as local. X-Forwarded-For is
// deliberately ignored: trusting it would let a remote caller claim 127.0.0.1.
func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return isLoopbackHost(strings.Trim(host, "[]"))
}

// isSameOriginRequest rejects cross-site POSTs. RDP launch and signing have
// side effects on the local desktop, so when a browser sends Origin it must
// match this host. Non-browser clients and same-origin form posts may omit it.
func isSameOriginRequest(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}
func fatal(msg string, err error) { fmt.Fprintln(os.Stderr, msg+":", err); os.Exit(1) }
