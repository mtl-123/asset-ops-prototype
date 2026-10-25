package guacamole

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newFakeGuacamole(t *testing.T) (*httptest.Server, *[]connectionPayload) {
	t.Helper()
	var saved []connectionPayload
	connections := map[string]connection{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/guacamole/api/tokens" && r.Method == http.MethodPost:
			_ = r.ParseForm()
			if r.Form.Get("username") != "admin" || r.Form.Get("password") != "pw" {
				http.Error(w, `{"message":"bad credentials"}`, http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(tokenResponse{AuthToken: "token-1", DataSource: "mysql"})
		case r.URL.Path == "/guacamole/api/session/data/mysql/connections" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(connections)
		case r.URL.Path == "/guacamole/api/session/data/mysql/connections" && r.Method == http.MethodPost:
			var payload connectionPayload
			_ = json.NewDecoder(r.Body).Decode(&payload)
			saved = append(saved, payload)
			connections["7"] = connection{Identifier: "7", Name: payload.Name, Protocol: payload.Protocol, Parameters: payload.Parameters}
			_ = json.NewEncoder(w).Encode(map[string]string{"identifier": "7"})
		case r.URL.Path == "/guacamole/api/session/data/mysql/connections/7" && r.Method == http.MethodPut:
			var payload connectionPayload
			_ = json.NewDecoder(r.Body).Decode(&payload)
			saved = append(saved, payload)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	return server, &saved
}

func TestEnsureConnectionCreatesAndUpdatesWithoutPasswordInURL(t *testing.T) {
	server, saved := newFakeGuacamole(t)
	defer server.Close()
	client, err := New(server.URL+"/guacamole/", "admin", "pw", "")
	if err != nil {
		t.Fatal(err)
	}
	parameters := map[string]string{
		"hostname": "10.0.0.4", "port": "3389", "username": `CORP\ops`, "password": "secret-password",
	}
	clientURL, err := client.EnsureConnection(t.Context(), "asset-1", parameters)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(clientURL, "secret-password") || strings.Contains(clientURL, `CORP\ops`) {
		t.Fatalf("client URL leaked credentials: %s", clientURL)
	}
	parsed, err := url.Parse(clientURL)
	if err != nil {
		t.Fatal(err)
	}
	encoded := strings.TrimPrefix(parsed.Fragment, "/client/")
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("identifier base64: %v", err)
	}
	if string(raw) != "7\x00c\x00mysql" {
		t.Fatalf("identifier fragment = %q", raw)
	}
	if len(*saved) != 1 || (*saved)[0].Parameters["password"] != "secret-password" {
		t.Fatalf("create payload: %#v", *saved)
	}
	if (*saved)[0].Parameters["security"] != "nla" || (*saved)[0].Parameters["ignore-cert"] != "true" {
		t.Fatalf("RDP defaults missing: %#v", (*saved)[0].Parameters)
	}

	// The second call finds the deterministic connection and updates it.
	parameters["password"] = "rotated-password"
	if _, err := client.EnsureConnection(t.Context(), "asset-1", parameters); err != nil {
		t.Fatal(err)
	}
	if len(*saved) != 2 || (*saved)[1].Parameters["password"] != "rotated-password" {
		t.Fatalf("update payload: %#v", *saved)
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	if _, err := New("ftp://example.com/", "admin", "pw", ""); err == nil {
		t.Fatal("invalid scheme must be rejected")
	}
	if _, err := New("https://example.com/guacamole/", "", "pw", ""); err == nil {
		t.Fatal("missing admin username must be rejected")
	}
}
