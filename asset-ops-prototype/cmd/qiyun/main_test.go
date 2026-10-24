package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

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
	return &app{store: st, ssh: gw, token: token, webRoot: dir}
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
