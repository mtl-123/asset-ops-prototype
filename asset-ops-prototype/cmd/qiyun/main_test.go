package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qiyun-asset-ops/internal/rdp"
	"qiyun-asset-ops/internal/secretbox"
	"qiyun-asset-ops/internal/sshgateway"
	"qiyun-asset-ops/internal/store"
)

func testApp(t *testing.T, token string) *app {
	t.Helper()
	dir := t.TempDir()
	box, err := secretbox.LoadOrCreate(filepath.Join(dir, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "test.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	gw, err := sshgateway.New(st, []string{"10.0.0.0/8"}, []string{"localhost:*"})
	if err != nil {
		t.Fatal(err)
	}
	// The RDP manager is real, but pinned to a non-Windows platform so these
	// handler tests never try to spawn PowerShell.
	rm, err := rdp.New(st, st, rdp.Options{DataDir: dir, Platform: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	return &app{store: st, ssh: gw, rdp: rm, token: token, webRoot: dir}
}

// The static handler must never serve runtime state. Before this allowlist existed,
// `-web .` made /data/master.key downloadable, which decrypts every credential.
func TestStaticHandlerHidesRuntimeStateAndSources(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.html", "<!doctype html><title>ok</title>")
	write("core.js", "console.log(1)")
	write("vendor/xterm/xterm.js", "/* lib */")
	write("assets/admin/avatar.png", "png")
	write("data/master.key", "c2VjcmV0LW1hc3Rlci1rZXk=")
	write("data/qiyun.db", "sqlite")
	write("internal/store/store.go", "package store")
	write("go.mod", "module x")
	write("README.md", "# docs")
	write("qa-evidence/shot.png", "png")

	handler := staticHandler(root)
	cases := map[string]int{
		"/":                        http.StatusOK,
		"/core.js":                 http.StatusOK,
		"/vendor/xterm/xterm.js":   http.StatusOK,
		"/assets/admin/avatar.png": http.StatusOK,
		"/index.html":              http.StatusMovedPermanently, // FileServer canonicalises to "/"
		"/data/master.key":         http.StatusNotFound,
		"/data/qiyun.db":           http.StatusNotFound,
		"/internal/store/store.go": http.StatusNotFound,
		"/go.mod":                  http.StatusNotFound,
		"/README.md":               http.StatusNotFound,
		"/qa-evidence/shot.png":    http.StatusNotFound,
		"/../data/master.key":      http.StatusNotFound,
	}
	for target, want := range cases {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != want {
			t.Errorf("%s: want %d, got %d", target, want, rec.Code)
		}
	}
}

func TestAPIAuthAndAssetSecretRedaction(t *testing.T) {
	a := testApp(t, "test-token")
	handler := a.routes()
	unauth := httptest.NewRecorder()
	handler.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/api/v1/assets", nil))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status %d", unauth.Code)
	}
	payload := map[string]any{"asset_code": "SRV-API-1", "name": "API server", "address": "10.20.0.8", "ssh_port": 22, "username": "ops", "auth_type": "password", "secret": "top-secret"}
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assets", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "top-secret") {
		t.Fatal("response leaked secret")
	}
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/assets", nil)
	listReq.Header.Set("Authorization", "Bearer test-token")
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), "SRV-API-1") {
		t.Fatalf("list failed: %d %s", listRec.Code, listRec.Body.String())
	}
}

// Full CRUD over HTTP: create, read, list, patch and delete must all work from the
// same API the UI uses.
func TestAssetCRUDOverHTTP(t *testing.T) {
	a := testApp(t, "")
	handler := a.routes()
	payload, _ := json.Marshal(map[string]any{"asset_code": "CRUD-1", "name": "crud", "address": "10.40.0.1", "ssh_port": 22, "username": "ops", "auth_type": "password", "secret": "pw", "department": "研发部"})
	create := httptest.NewRequest(http.MethodPost, "/api/v1/assets", bytes.NewReader(payload))
	create.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, create)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create %d: %s", createRec.Code, createRec.Body.String())
	}
	var created store.Asset
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	read := httptest.NewRequest(http.MethodGet, "/api/v1/assets/"+created.ID, nil)
	readRec := httptest.NewRecorder()
	handler.ServeHTTP(readRec, read)
	if readRec.Code != http.StatusOK {
		t.Fatalf("read %d", readRec.Code)
	}

	list := httptest.NewRequest(http.MethodGet, "/api/v1/assets?q=crud&department=研发部", nil)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, list)
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), "CRUD-1") {
		t.Fatalf("list with filters failed: %d %s", listRec.Code, listRec.Body.String())
	}

	patch, _ := json.Marshal(map[string]any{"name": "renamed", "owner": "张三", "price_cents": 9900})
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/v1/assets/"+created.ID, bytes.NewReader(patch))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRec := httptest.NewRecorder()
	handler.ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch %d: %s", patchRec.Code, patchRec.Body.String())
	}
	var patched store.Asset
	if err := json.Unmarshal(patchRec.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	if patched.Name != "renamed" || patched.Owner != "张三" || patched.PriceCents != 9900 {
		t.Fatalf("patch not applied: %#v", patched)
	}

	badPatch := httptest.NewRequest(http.MethodPatch, "/api/v1/assets/"+created.ID, bytes.NewReader([]byte(`{"unknown_field":1}`)))
	badRec := httptest.NewRecorder()
	handler.ServeHTTP(badRec, badPatch)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field must be rejected, got %d", badRec.Code)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/assets/"+created.ID, nil)
	deleteRec := httptest.NewRecorder()
	handler.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete %d", deleteRec.Code)
	}
	gone := httptest.NewRequest(http.MethodGet, "/api/v1/assets/"+created.ID, nil)
	goneRec := httptest.NewRecorder()
	handler.ServeHTTP(goneRec, gone)
	if goneRec.Code != http.StatusNotFound {
		t.Fatalf("deleted asset must be 404, got %d", goneRec.Code)
	}
}

// The browser posts {asset_id, cols, rows}. The strict decoder rejected "cols"
// and the whole terminal request failed with 400 while looking like success in the
// logs, so this guards the exact request shape the frontend sends.
func TestCreateTerminalSessionAcceptsBrowserPayload(t *testing.T) {
	a := testApp(t, "")
	ctx := context.Background()
	asset, err := a.store.CreateAsset(ctx, store.CreateAsset{AssetCode: "WS-1", Name: "ws", Address: "10.30.0.9", SSHPort: 22, Username: "ops", AuthType: "password", Secret: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.store.ApproveHostKey(ctx, asset.ID, "ssh-ed25519 AAAATEST", "SHA256:test"); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"asset_id": asset.ID, "cols": 160, "rows": 40})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/terminal-sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("terminal session status %d: %s", rec.Code, rec.Body.String())
	}
	var session struct {
		ID      string `json:"id"`
		Ticket  string `json:"ws_ticket"`
		Expires string `json:"expires_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.ID == "" || session.Ticket == "" || session.Expires == "" {
		t.Fatalf("incomplete session response: %s", rec.Body.String())
	}
}

// An unapproved host key must produce an actionable 409 instead of a generic error.
func TestCreateTerminalSessionRequiresApprovedHostKey(t *testing.T) {
	a := testApp(t, "")
	asset, err := a.store.CreateAsset(context.Background(), store.CreateAsset{AssetCode: "WS-2", Name: "ws", Address: "10.30.0.10", SSHPort: 22, Username: "ops", AuthType: "password", Secret: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"asset_id": asset.ID, "cols": 120, "rows": 32})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/terminal-sessions", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	a.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "HOST_KEY_UNKNOWN") {
		t.Fatalf("expected 409 HOST_KEY_UNKNOWN, got %d %s", rec.Code, rec.Body.String())
	}
}

// Windows assets are managed through RDP: the stored asset must carry the RDP
// port and family, SSH terminal creation must be refused with a useful code, and
// the launch endpoint must reject remote callers before touching the asset.
func TestWindowsAssetAndRDPGuards(t *testing.T) {
	a := testApp(t, "")
	handler := a.routes()
	payload, _ := json.Marshal(map[string]any{
		"asset_code": "WIN-RDS-1", "name": "win-rds-01", "address": "10.20.30.33",
		"ssh_port": 22, "rdp_port": 3390, "os_name": "Windows Server 2022",
		"asset_type": "windows_host", "username": `CORP\ops`, "auth_type": "password", "secret": "pw",
		"department": "IT",
	})
	create := httptest.NewRequest(http.MethodPost, "/api/v1/assets", bytes.NewReader(payload))
	create.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, create)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create windows asset %d: %s", createRec.Code, createRec.Body.String())
	}
	var asset store.Asset
	if err := json.Unmarshal(createRec.Body.Bytes(), &asset); err != nil {
		t.Fatal(err)
	}
	if asset.RDPPort != 3390 || asset.OSFamily != "windows" || asset.AssetType != "windows_host" || !asset.IsWindows() {
		t.Fatalf("windows asset not normalised: %#v", asset)
	}

	sshBody, _ := json.Marshal(map[string]any{"asset_id": asset.ID})
	sshReq := httptest.NewRequest(http.MethodPost, "/api/v1/terminal-sessions", bytes.NewReader(sshBody))
	sshRec := httptest.NewRecorder()
	handler.ServeHTTP(sshRec, sshReq)
	if sshRec.Code != http.StatusConflict || !strings.Contains(sshRec.Body.String(), "RDP_ASSET") {
		t.Fatalf("windows asset must not open an SSH terminal: %d %s", sshRec.Code, sshRec.Body.String())
	}

	remote := httptest.NewRequest(http.MethodPost, "/api/v1/assets/"+asset.ID+"/rdp/launch", bytes.NewReader([]byte(`{}`)))
	remote.RemoteAddr = "203.0.113.9:5555"
	remoteRec := httptest.NewRecorder()
	handler.ServeHTTP(remoteRec, remote)
	if remoteRec.Code != http.StatusForbidden || !strings.Contains(remoteRec.Body.String(), "RDP_LOCAL_WINDOWS_ONLY") {
		t.Fatalf("remote RDP launch must be refused: %d %s", remoteRec.Code, remoteRec.Body.String())
	}

	cross := httptest.NewRequest(http.MethodPost, "/api/v1/assets/"+asset.ID+"/rdp/launch", bytes.NewReader([]byte(`{}`)))
	cross.RemoteAddr = "127.0.0.1:5555"
	cross.Header.Set("Origin", "https://evil.example")
	crossRec := httptest.NewRecorder()
	handler.ServeHTTP(crossRec, cross)
	if crossRec.Code != http.StatusForbidden || !strings.Contains(crossRec.Body.String(), "RDP_CROSS_ORIGIN") {
		t.Fatalf("cross-origin RDP launch must be refused: %d %s", crossRec.Code, crossRec.Body.String())
	}

	local := httptest.NewRequest(http.MethodPost, "/api/v1/assets/"+asset.ID+"/rdp/launch", bytes.NewReader([]byte(`{}`)))
	local.RemoteAddr = "127.0.0.1:5555"
	localRec := httptest.NewRecorder()
	handler.ServeHTTP(localRec, local)
	if localRec.Code != http.StatusNotImplemented || !strings.Contains(localRec.Body.String(), "RDP_PLATFORM_UNSUPPORTED") {
		t.Fatalf("non-Windows service must explain the platform limit: %d %s", localRec.Code, localRec.Body.String())
	}
}

func TestWindowsAssetRejectsPrivateKeyCredential(t *testing.T) {
	a := testApp(t, "")
	_, err := a.store.CreateAsset(context.Background(), store.CreateAsset{
		AssetCode: "WIN-KEY", Name: "win-key", Address: "10.20.30.34",
		OSName: "Windows Server 2022", AssetType: "windows_host",
		Username: "ops", AuthType: "private_key", Secret: "not-a-key",
	})
	if err == nil || !strings.Contains(err.Error(), "远程桌面") {
		t.Fatalf("expected RDP password explanation, got %v", err)
	}
}
