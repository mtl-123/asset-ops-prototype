package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"qiyun-asset-ops/internal/auth"
	"qiyun-asset-ops/internal/rdp"
	"qiyun-asset-ops/internal/store"
)

func newAuthTestApp(t *testing.T) *app {
	t.Helper()
	a := testApp(t, "")
	a.authEnabled = true
	a.sessionTTL = time.Hour
	a.limiter = newLoginLimiter()
	hash, err := auth.HashPassword("AdminPass123!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.CreateUser(context.Background(), "admin", "系统管理员", hash, auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	return a
}

func performJSON(t *testing.T, handler http.Handler, method, path string, body any, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	request := httptest.NewRequest(method, path, reader)
	request.RemoteAddr = "127.0.0.1:4321"
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func loginAs(t *testing.T, handler http.Handler, username, password string) (*http.Cookie, string, string) {
	t.Helper()
	recorder := performJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"username": username, "password": password}, nil, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("login %s: %d %s", username, recorder.Code, recorder.Body.String())
	}
	var payload struct {
		CSRF string `json:"csrf_token"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			return cookie, payload.CSRF, payload.User.ID
		}
	}
	t.Fatal("session cookie missing")
	return nil, "", ""
}

func createTestAsset(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, code string) store.Asset {
	t.Helper()
	recorder := performJSON(t, handler, http.MethodPost, "/api/v1/assets", map[string]any{
		"asset_code": code, "name": code, "address": "10.0.0.90", "ssh_port": 22,
		"os_name": "Linux", "asset_type": "linux_host", "username": "root",
		"auth_type": "password", "secret": "x", "department": "IT",
	}, cookie, csrf)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create asset: %d %s", recorder.Code, recorder.Body.String())
	}
	var asset store.Asset
	if err := json.Unmarshal(recorder.Body.Bytes(), &asset); err != nil {
		t.Fatal(err)
	}
	return asset
}

func TestAuthLoginAndAssetPermissions(t *testing.T) {
	a := newAuthTestApp(t)
	handler := a.routes()

	if recorder := performJSON(t, handler, http.MethodGet, "/api/v1/auth/me", nil, nil, ""); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("me without session must be 401, got %d", recorder.Code)
	}

	adminCookie, adminCSRF, adminID := loginAs(t, handler, "admin", "AdminPass123!")
	me := performJSON(t, handler, http.MethodGet, "/api/v1/auth/me", nil, adminCookie, "")
	if me.Code != http.StatusOK {
		t.Fatalf("me: %d %s", me.Code, me.Body.String())
	}

	createUser := performJSON(t, handler, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "ops1", "display_name": "运维", "password": "OpsPass123!", "role": "operator",
	}, adminCookie, adminCSRF)
	if createUser.Code != http.StatusCreated {
		t.Fatalf("create operator: %d %s", createUser.Code, createUser.Body.String())
	}
	var operator store.User
	if err := json.Unmarshal(createUser.Body.Bytes(), &operator); err != nil {
		t.Fatal(err)
	}

	granted := createTestAsset(t, handler, adminCookie, adminCSRF, "AUTH-GRANTED")
	other := createTestAsset(t, handler, adminCookie, adminCSRF, "AUTH-OTHER")
	grant := performJSON(t, handler, http.MethodPut, "/api/v1/assets/"+granted.ID+"/grants", map[string]any{
		"grants": []map[string]any{{"user_id": operator.ID, "can_connect": true, "can_edit": false}},
	}, adminCookie, adminCSRF)
	if grant.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", grant.Code, grant.Body.String())
	}

	opsCookie, opsCSRF, _ := loginAs(t, handler, "ops1", "OpsPass123!")
	list := performJSON(t, handler, http.MethodGet, "/api/v1/assets", nil, opsCookie, "")
	if list.Code != http.StatusOK {
		t.Fatalf("ops list: %d %s", list.Code, list.Body.String())
	}
	var listed struct {
		Items []store.Asset `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 1 || listed.Items[0].ID != granted.ID {
		t.Fatalf("operator must only see the granted asset, got %#v", listed.Items)
	}

	if recorder := performJSON(t, handler, http.MethodGet, "/api/v1/assets/"+other.ID, nil, opsCookie, ""); recorder.Code != http.StatusNotFound {
		t.Fatalf("ungranted read must be 404, got %d", recorder.Code)
	}
	if recorder := performJSON(t, handler, http.MethodPatch, "/api/v1/assets/"+granted.ID, map[string]any{"name": "x"}, opsCookie, opsCSRF); recorder.Code != http.StatusNotFound {
		t.Fatalf("operator without can_edit must be 404, got %d", recorder.Code)
	}
	if recorder := performJSON(t, handler, http.MethodGet, "/api/v1/users", nil, opsCookie, ""); recorder.Code != http.StatusForbidden {
		t.Fatalf("operator user list must be 403, got %d", recorder.Code)
	}
	if recorder := performJSON(t, handler, http.MethodPost, "/api/v1/terminal-sessions", map[string]any{"asset_id": granted.ID}, opsCookie, ""); recorder.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF must be 403, got %d", recorder.Code)
	}
	if recorder := performJSON(t, handler, http.MethodPost, "/api/v1/terminal-sessions", map[string]any{"asset_id": granted.ID}, opsCookie, opsCSRF); recorder.Code != http.StatusConflict {
		t.Fatalf("granted SSH attempt should reach host-key check (409), got %d %s", recorder.Code, recorder.Body.String())
	}

	if recorder := performJSON(t, handler, http.MethodPatch, "/api/v1/users/"+adminID, map[string]any{"role": "viewer"}, adminCookie, adminCSRF); recorder.Code != http.StatusConflict {
		t.Fatalf("demoting the last admin must be 409, got %d %s", recorder.Code, recorder.Body.String())
	}
	audit := performJSON(t, handler, http.MethodGet, "/api/v1/audit?limit=50", nil, adminCookie, "")
	if audit.Code != http.StatusOK {
		t.Fatalf("admin audit: %d %s", audit.Code, audit.Body.String())
	}
	var auditBody struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(audit.Body.Bytes(), &auditBody); err != nil {
		t.Fatal(err)
	}
	if auditBody.Count == 0 {
		t.Fatal("audit events must include login/asset/grant actions")
	}
	if recorder := performJSON(t, handler, http.MethodGet, "/api/v1/audit", nil, opsCookie, ""); recorder.Code != http.StatusForbidden {
		t.Fatalf("operator audit must be 403, got %d", recorder.Code)
	}
}

func createWindowsTestAsset(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, code string) store.Asset {
	t.Helper()
	recorder := performJSON(t, handler, http.MethodPost, "/api/v1/assets", map[string]any{
		"asset_code": code, "name": code, "address": "10.0.0.91", "ssh_port": 22, "rdp_port": 3389,
		"os_name": "Windows Server 2022", "asset_type": "windows_host", "username": `CORP\ops`,
		"auth_type": "password", "secret": "pw", "department": "IT",
	}, cookie, csrf)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create windows asset: %d %s", recorder.Code, recorder.Body.String())
	}
	var asset store.Asset
	if err := json.Unmarshal(recorder.Body.Bytes(), &asset); err != nil {
		t.Fatal(err)
	}
	return asset
}

func TestRDPTicketCreateAndRedeem(t *testing.T) {
	a := newAuthTestApp(t)
	handler := a.routes()
	adminCookie, adminCSRF, _ := loginAs(t, handler, "admin", "AdminPass123!")
	create := performJSON(t, handler, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "ops1", "password": "OpsPass123!", "role": "operator",
	}, adminCookie, adminCSRF)
	if create.Code != http.StatusCreated {
		t.Fatalf("create operator: %d %s", create.Code, create.Body.String())
	}
	var operator store.User
	if err := json.Unmarshal(create.Body.Bytes(), &operator); err != nil {
		t.Fatal(err)
	}
	windowsAsset := createWindowsTestAsset(t, handler, adminCookie, adminCSRF, "RDP-TICKET")
	linuxAsset := createTestAsset(t, handler, adminCookie, adminCSRF, "RDP-LINUX")
	performJSON(t, handler, http.MethodPut, "/api/v1/assets/"+windowsAsset.ID+"/grants", map[string]any{
		"grants": []map[string]any{{"user_id": operator.ID, "can_connect": true}},
	}, adminCookie, adminCSRF)
	opsCookie, opsCSRF, _ := loginAs(t, handler, "ops1", "OpsPass123!")

	if recorder := performJSON(t, handler, http.MethodPost, "/api/v1/assets/"+linuxAsset.ID+"/rdp/ticket", map[string]any{}, opsCookie, opsCSRF); recorder.Code != http.StatusNotFound {
		t.Fatalf("ungranted linux asset ticket must be 404, got %d", recorder.Code)
	}
	createTicket := performJSON(t, handler, http.MethodPost, "/api/v1/assets/"+windowsAsset.ID+"/rdp/ticket", map[string]any{"port": 3389}, opsCookie, opsCSRF)
	if createTicket.Code != http.StatusOK {
		t.Fatalf("create ticket: %d %s", createTicket.Code, createTicket.Body.String())
	}
	var ticketPayload struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(createTicket.Body.Bytes(), &ticketPayload); err != nil || ticketPayload.Ticket == "" {
		t.Fatalf("ticket missing: %v %s", err, createTicket.Body.String())
	}
	redeem := httptest.NewRequest(http.MethodPost, "/api/v1/rdp/tickets/redeem", nil)
	redeem.RemoteAddr = "127.0.0.1:4321"
	redeem.Header.Set("Authorization", "Bearer "+ticketPayload.Ticket)
	redeemRecorder := httptest.NewRecorder()
	handler.ServeHTTP(redeemRecorder, redeem)
	if redeemRecorder.Code != http.StatusOK {
		t.Fatalf("redeem: %d %s", redeemRecorder.Code, redeemRecorder.Body.String())
	}
	var profile rdp.Profile
	if err := json.Unmarshal(redeemRecorder.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if profile.Address != windowsAsset.Address || profile.Port != 3389 || profile.Username != `CORP\ops` || profile.Password != "pw" {
		t.Fatalf("unexpected redeemed profile: %#v", profile)
	}
	second := httptest.NewRequest(http.MethodPost, "/api/v1/rdp/tickets/redeem", nil)
	second.Header.Set("Authorization", "Bearer "+ticketPayload.Ticket)
	secondRecorder := httptest.NewRecorder()
	handler.ServeHTTP(secondRecorder, second)
	if secondRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("ticket must be single-use, got %d", secondRecorder.Code)
	}
}

func TestViewerCannotConnect(t *testing.T) {
	a := newAuthTestApp(t)
	handler := a.routes()
	adminCookie, adminCSRF, _ := loginAs(t, handler, "admin", "AdminPass123!")
	create := performJSON(t, handler, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "viewer1", "password": "ViewPass123!", "role": "viewer",
	}, adminCookie, adminCSRF)
	if create.Code != http.StatusCreated {
		t.Fatalf("create viewer: %d %s", create.Code, create.Body.String())
	}
	var viewer store.User
	if err := json.Unmarshal(create.Body.Bytes(), &viewer); err != nil {
		t.Fatal(err)
	}
	asset := createTestAsset(t, handler, adminCookie, adminCSRF, "AUTH-VIEW")
	performJSON(t, handler, http.MethodPut, "/api/v1/assets/"+asset.ID+"/grants", map[string]any{
		"grants": []map[string]any{{"user_id": viewer.ID, "can_connect": true}},
	}, adminCookie, adminCSRF)
	viewerCookie, viewerCSRF, _ := loginAs(t, handler, "viewer1", "ViewPass123!")
	if recorder := performJSON(t, handler, http.MethodPost, "/api/v1/terminal-sessions", map[string]any{"asset_id": asset.ID}, viewerCookie, viewerCSRF); recorder.Code != http.StatusForbidden {
		t.Fatalf("viewer must not have ssh.connect, got %d %s", recorder.Code, recorder.Body.String())
	}
}
