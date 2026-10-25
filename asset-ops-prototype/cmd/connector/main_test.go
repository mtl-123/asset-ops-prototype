//go:build windows

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"qiyun-asset-ops/internal/rdp"
)

func newTestServer(t *testing.T, central *httptest.Server) *server {
	t.Helper()
	manager, err := rdp.NewLocal(rdp.Options{DataDir: t.TempDir(), Platform: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	return &server{
		manager: manager, central: central.URL, client: central.Client(),
		origins: map[string]bool{"https://ops.example.com": true},
	}
}

func TestConnectorCORSAndLaunch(t *testing.T) {
	var redeemed string
	central := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/rdp/tickets/redeem" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		redeemed = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rdp.Profile{Address: "10.0.0.4", Port: 3389, Username: `CORP\ops`, Password: "pw"})
	}))
	defer central.Close()
	s := newTestServer(t, central)
	handler := s.cors(s.routes())

	health := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	health.Header.Set("Origin", "https://ops.example.com")
	healthRecorder := httptest.NewRecorder()
	handler.ServeHTTP(healthRecorder, health)
	if healthRecorder.Code != http.StatusOK {
		t.Fatalf("health: %d %s", healthRecorder.Code, healthRecorder.Body.String())
	}
	if healthRecorder.Header().Get("Access-Control-Allow-Origin") != "https://ops.example.com" {
		t.Fatal("allowed origin did not receive CORS header")
	}

	blocked := httptest.NewRequest(http.MethodPost, "/launch", strings.NewReader(`{"ticket":"ticket-1"}`))
	blocked.Header.Set("Origin", "https://evil.example")
	blockedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(blockedRecorder, blocked)
	if blockedRecorder.Code != http.StatusForbidden {
		t.Fatalf("unknown origin must be 403, got %d", blockedRecorder.Code)
	}

	noHeader := httptest.NewRequest(http.MethodPost, "/launch", strings.NewReader(`{"ticket":"ticket-1"}`))
	noHeader.Header.Set("Origin", "https://ops.example.com")
	noHeaderRecorder := httptest.NewRecorder()
	handler.ServeHTTP(noHeaderRecorder, noHeader)
	if noHeaderRecorder.Code != http.StatusForbidden {
		t.Fatalf("POST without connector header must be 403, got %d", noHeaderRecorder.Code)
	}

	launch := httptest.NewRequest(http.MethodPost, "/launch", strings.NewReader(`{"ticket":"ticket-1"}`))
	launch.Header.Set("Origin", "https://ops.example.com")
	launch.Header.Set("X-Qiyun-Connector", "1")
	launch.Header.Set("Content-Type", "application/json")
	launchRecorder := httptest.NewRecorder()
	handler.ServeHTTP(launchRecorder, launch)
	if redeemed != "ticket-1" {
		t.Fatalf("central did not receive the bearer ticket: %q", redeemed)
	}
	if launchRecorder.Code != http.StatusNotImplemented || !strings.Contains(launchRecorder.Body.String(), "RDP_PLATFORM_UNSUPPORTED") {
		t.Fatalf("linux test manager must reject launch after redeem: %d %s", launchRecorder.Code, launchRecorder.Body.String())
	}

	preflight := httptest.NewRequest(http.MethodOptions, "/launch", nil)
	preflight.Header.Set("Origin", "https://ops.example.com")
	preflight.Header.Set("Access-Control-Request-Method", "POST")
	preflight.Header.Set("Access-Control-Request-Private-Network", "true")
	preflightRecorder := httptest.NewRecorder()
	handler.ServeHTTP(preflightRecorder, preflight)
	if preflightRecorder.Code != http.StatusNoContent {
		t.Fatalf("preflight: %d", preflightRecorder.Code)
	}
	if preflightRecorder.Header().Get("Access-Control-Allow-Private-Network") != "true" {
		t.Fatal("private network preflight header missing")
	}
}
