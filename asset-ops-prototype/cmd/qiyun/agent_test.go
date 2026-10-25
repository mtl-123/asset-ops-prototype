package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func agentPayload() []byte {
	body := map[string]any{
		"schema_version": 1,
		"agent_version":  "0.1.0",
		"host_id":        "test-agent-1",
		"collected_at":   "2026-10-25T10:00:00Z",
		"host": map[string]any{
			"hostname":       "pve-test",
			"os_name":        "Debian GNU/Linux",
			"uptime_seconds": 120,
			"cpu":            map[string]any{"usage_percent": 10.5, "logical_cores": 8, "physical_cores": 4},
			"memory":         map[string]any{"usage_percent": 42.0, "total_bytes": 1024, "used_bytes": 430},
			"disks": []map[string]any{
				{"mount": "/", "usage_percent": 55.0, "total_bytes": 100, "used_bytes": 55},
			},
		},
		"users": map[string]any{"logged_in_count": 2, "session_count": 3, "stale_count": 1},
		"platforms": map[string]any{
			"pve": map[string]any{
				"available":  true,
				"node":       "pve-test",
				"vms":        []map[string]any{{"vmid": 101, "name": "win-1"}},
				"containers": []map[string]any{{"vmid": 200, "name": "ct-1"}},
			},
			"docker":     map[string]any{"available": true, "containers_total": 4, "containers_running": 3},
			"kubernetes": map[string]any{"available": true, "node_count": 2, "pod_count": 7},
		},
	}
	raw, _ := json.Marshal(body)
	return raw
}

func TestAgentReportIngestAndQuery(t *testing.T) {
	a := testApp(t, "")
	a.agentToken = "agent-secret"
	handler := a.routes()

	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/report", bytes.NewReader(agentPayload()))
	request.Header.Set("Authorization", "Bearer agent-secret")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("agent report status %d: %s", recorder.Code, recorder.Body.String())
	}

	list := httptest.NewRequest(http.MethodGet, "/api/v1/agent/hosts", nil)
	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, list)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("agent list status %d: %s", listRecorder.Code, listRecorder.Body.String())
	}
	var listBody struct {
		Items []struct {
			HostID      string  `json:"host_id"`
			Hostname    string  `json:"hostname"`
			CPUPercent  float64 `json:"cpu_percent"`
			DiskPercent float64 `json:"disk_percent"`
			PveVMs      int     `json:"pve_vms"`
		} `json:"items"`
		Total    int `json:"total"`
		Page     int `json:"page"`
		PageSize int `json:"page_size"`
		Pages    int `json:"pages"`
		Summary  struct {
			Hosts int `json:"hosts"`
			Fresh int `json:"fresh"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listBody); err != nil {
		t.Fatal(err)
	}
	if len(listBody.Items) != 1 || listBody.Items[0].HostID != "test-agent-1" {
		t.Fatalf("unexpected list: %s", listRecorder.Body.String())
	}
	if listBody.Total != 1 || listBody.Page != 1 || listBody.PageSize != 50 || listBody.Pages != 1 {
		t.Fatalf("pagination metadata incorrect: %#v", listBody)
	}
	if listBody.Summary.Hosts != 1 || listBody.Summary.Fresh != 1 {
		t.Fatalf("fleet summary incorrect: %#v", listBody.Summary)
	}
	if listBody.Items[0].DiskPercent != 55 || listBody.Items[0].PveVMs != 1 {
		t.Fatalf("summary fields incorrect: %#v", listBody.Items[0])
	}

	filtered := httptest.NewRequest(http.MethodGet, "/api/v1/agent/hosts?page=1&page_size=1&q=pve-test&status=fresh&platform=pve&sort=cpu", nil)
	filteredRecorder := httptest.NewRecorder()
	handler.ServeHTTP(filteredRecorder, filtered)
	if filteredRecorder.Code != http.StatusOK {
		t.Fatalf("filtered agent list status %d: %s", filteredRecorder.Code, filteredRecorder.Body.String())
	}
	var filteredBody struct {
		Items []struct {
			HostID string `json:"host_id"`
		} `json:"items"`
		Total    int `json:"total"`
		PageSize int `json:"page_size"`
	}
	if err := json.Unmarshal(filteredRecorder.Body.Bytes(), &filteredBody); err != nil {
		t.Fatal(err)
	}
	if filteredBody.Total != 1 || filteredBody.PageSize != 1 || len(filteredBody.Items) != 1 || filteredBody.Items[0].HostID != "test-agent-1" {
		t.Fatalf("filtered list incorrect: %s", filteredRecorder.Body.String())
	}

	detail := httptest.NewRequest(http.MethodGet, "/api/v1/agent/hosts/test-agent-1", nil)
	detailRecorder := httptest.NewRecorder()
	handler.ServeHTTP(detailRecorder, detail)
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("agent detail status %d: %s", detailRecorder.Code, detailRecorder.Body.String())
	}
	if !strings.Contains(detailRecorder.Body.String(), `"hostname":"pve-test"`) {
		t.Fatalf("detail payload missing hostname: %s", detailRecorder.Body.String())
	}
	if !strings.Contains(detailRecorder.Body.String(), `"vmid":101`) {
		t.Fatalf("detail payload missing VM: %s", detailRecorder.Body.String())
	}
}

func TestAgentReportRejectsBadTokenAndInvalidBody(t *testing.T) {
	a := testApp(t, "")
	a.agentToken = "agent-secret"
	handler := a.routes()

	badToken := httptest.NewRequest(http.MethodPost, "/api/v1/agent/report", bytes.NewReader(agentPayload()))
	badToken.Header.Set("Authorization", "Bearer wrong")
	badTokenRecorder := httptest.NewRecorder()
	handler.ServeHTTP(badTokenRecorder, badToken)
	if badTokenRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("bad token status %d: %s", badTokenRecorder.Code, badTokenRecorder.Body.String())
	}

	invalid := httptest.NewRequest(http.MethodPost, "/api/v1/agent/report", strings.NewReader(`{"schema_version":1}`))
	invalid.Header.Set("Authorization", "Bearer agent-secret")
	invalidRecorder := httptest.NewRecorder()
	handler.ServeHTTP(invalidRecorder, invalid)
	if invalidRecorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid body status %d: %s", invalidRecorder.Code, invalidRecorder.Body.String())
	}

	a.agentToken = ""
	unconfigured := httptest.NewRequest(http.MethodPost, "/api/v1/agent/report", bytes.NewReader(agentPayload()))
	unconfiguredRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unconfiguredRecorder, unconfigured)
	if unconfiguredRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured status %d: %s", unconfiguredRecorder.Code, unconfiguredRecorder.Body.String())
	}
}

func TestAgentEnrollment(t *testing.T) {
	a := testApp(t, "")
	a.agentToken = "agent-secret"
	handler := a.routes()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/agent/enrollment", nil)
	request.Host = "127.0.0.1:8765"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("enrollment status %d: %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		ServerURL   string `json:"server_url"`
		Token       string `json:"token"`
		TokenFile   string `json:"token_file"`
		Platform    string `json:"platform"`
		LocalOnly   bool   `json:"local_only"`
		DownloadURL string `json:"download_url"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Token != "agent-secret" || body.ServerURL != "http://127.0.0.1:8765" || body.Platform != "linux-amd64" {
		t.Fatalf("unexpected enrollment: %#v", body)
	}
	if !body.LocalOnly {
		t.Fatalf("loopback enrollment should be marked local_only: %#v", body)
	}
	if body.TokenFile != "data/agent.token" {
		t.Fatalf("unexpected token file: %#v", body)
	}
}

func TestAgentHostMissing(t *testing.T) {
	a := testApp(t, "")
	a.agentToken = "agent-secret"
	handler := a.routes()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/agent/hosts/does-not-exist", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing host status %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestAgentHostCRUDAndBulkDelete(t *testing.T) {
	a := testApp(t, "")
	a.agentToken = "agent-secret"
	handler := a.routes()

	create := func(id, hostname string) {
		body, _ := json.Marshal(map[string]any{
			"host_id": id, "hostname": hostname, "display_name": "显示 " + hostname,
			"note": "登记备注", "tags": []string{"生产", "PVE"},
		})
		request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/hosts", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("create %s status %d: %s", id, recorder.Code, recorder.Body.String())
		}
	}

	create("manual-1", "manual-01")
	create("manual-2", "manual-02")

	list := httptest.NewRequest(http.MethodGet, "/api/v1/agent/hosts?q=manual&page_size=1", nil)
	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, list)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", listRecorder.Code, listRecorder.Body.String())
	}
	var listBody struct {
		Items []struct {
			HostID      string   `json:"host_id"`
			DisplayName string   `json:"display_name"`
			Tags        []string `json:"tags"`
		} `json:"items"`
		Total    int `json:"total"`
		PageSize int `json:"page_size"`
	}
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listBody); err != nil {
		t.Fatal(err)
	}
	if listBody.Total != 2 || listBody.PageSize != 1 || len(listBody.Items) != 1 {
		t.Fatalf("manual list incorrect: %s", listRecorder.Body.String())
	}
	if listBody.Items[0].DisplayName == "" || len(listBody.Items[0].Tags) != 2 {
		t.Fatalf("manual metadata missing: %#v", listBody.Items[0])
	}

	patchBody := []byte(`{"display_name":"改名","note":"新备注","tags":["测试"]}`)
	patch := httptest.NewRequest(http.MethodPatch, "/api/v1/agent/hosts/manual-1", bytes.NewReader(patchBody))
	patch.Header.Set("Content-Type", "application/json")
	patchRecorder := httptest.NewRecorder()
	handler.ServeHTTP(patchRecorder, patch)
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("patch status %d: %s", patchRecorder.Code, patchRecorder.Body.String())
	}
	detail := httptest.NewRequest(http.MethodGet, "/api/v1/agent/hosts/manual-1", nil)
	detailRecorder := httptest.NewRecorder()
	handler.ServeHTTP(detailRecorder, detail)
	if detailRecorder.Code != http.StatusOK || !strings.Contains(detailRecorder.Body.String(), `"display_name":"改名"`) {
		t.Fatalf("patched detail missing metadata: %s", detailRecorder.Body.String())
	}

	bulkBody := []byte(`{"ids":["manual-1","manual-2"]}`)
	bulk := httptest.NewRequest(http.MethodPost, "/api/v1/agent/hosts/bulk-delete", bytes.NewReader(bulkBody))
	bulk.Header.Set("Content-Type", "application/json")
	bulkRecorder := httptest.NewRecorder()
	handler.ServeHTTP(bulkRecorder, bulk)
	if bulkRecorder.Code != http.StatusOK || !strings.Contains(bulkRecorder.Body.String(), `"deleted":2`) {
		t.Fatalf("bulk delete failed: %d %s", bulkRecorder.Code, bulkRecorder.Body.String())
	}
	listAgain := httptest.NewRequest(http.MethodGet, "/api/v1/agent/hosts", nil)
	listAgainRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listAgainRecorder, listAgain)
	if !strings.Contains(listAgainRecorder.Body.String(), `"total":0`) {
		t.Fatalf("nodes remain after bulk delete: %s", listAgainRecorder.Body.String())
	}
}
