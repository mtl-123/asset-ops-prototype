package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"qiyun-asset-ops/internal/guacamole"
)

func TestWebRDPCreatesGuacamoleConnection(t *testing.T) {
	guacServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/guacamole/api/tokens":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"authToken": "guac-token", "dataSource": "mysql"})
		case r.URL.Path == "/guacamole/api/session/data/mysql/connections" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/guacamole/api/session/data/mysql/connections" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"identifier":"9"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer guacServer.Close()

	a := newAuthTestApp(t)
	client, err := guacamole.New(guacServer.URL+"/guacamole/", "admin", "pw", "")
	if err != nil {
		t.Fatal(err)
	}
	a.guac = client
	handler := a.routes()
	adminCookie, adminCSRF, _ := loginAs(t, handler, "admin", "AdminPass123!")
	asset := createWindowsTestAsset(t, handler, adminCookie, adminCSRF, "WEB-RDP-1")

	response := performJSON(t, handler, http.MethodPost, "/api/v1/assets/"+asset.ID+"/web-rdp", map[string]any{}, adminCookie, adminCSRF)
	if response.Code != http.StatusOK {
		t.Fatalf("web-rdp: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload.URL, "pw") || strings.Contains(payload.URL, `CORP\ops`) {
		t.Fatalf("client URL leaked credentials: %s", payload.URL)
	}
	parsed, err := url.Parse(payload.URL)
	if err != nil {
		t.Fatal(err)
	}
	encoded := strings.TrimPrefix(parsed.Fragment, "/client/")
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "9\x00c\x00mysql" {
		t.Fatalf("client identifier = %q", raw)
	}
}
