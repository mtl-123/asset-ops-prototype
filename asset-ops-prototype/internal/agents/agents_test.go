package agents

import (
	"encoding/json"
	"testing"
	"time"
)

func sampleReport() Report {
	return Report{
		SchemaVersion: 1,
		AgentVersion:  "0.1.0",
		HostID:        "host-1",
		CollectedAt:   "2026-10-25T10:00:00Z",
		Host: Host{
			Hostname:      "pve-1",
			OSName:        "Debian GNU/Linux",
			UptimeSeconds: 3600,
			CPU:           CPU{UsagePercent: 12.5, LogicalCores: 16, PhysicalCores: 8},
			Memory:        Memory{TotalBytes: 64 << 30, UsedBytes: 32 << 30, UsagePercent: 50},
			Disks: []Disk{
				{Mount: "/", UsagePercent: 40, TotalBytes: 100, UsedBytes: 40},
				{Mount: "/data", UsagePercent: 80, TotalBytes: 200, UsedBytes: 160},
			},
		},
		Users: Users{LoggedInCount: 3, SessionCount: 4, StaleCount: 2, LocalUserCount: 10, LastlogAvailable: true},
		Platforms: Platforms{
			Pve: Pve{Available: true, VMs: []json.RawMessage{
				json.RawMessage(`{"vmid":101}`), json.RawMessage(`{"vmid":102}`),
			}, Containers: []json.RawMessage{json.RawMessage(`{"vmid":200}`)}},
			Docker:     Docker{Available: true, ContainersTotal: 6, ContainersRunning: 4},
			Kubernetes: Kubernetes{Available: true, NodeCount: 3, NodesReady: 2, PodCount: 10},
		},
	}
}

func TestReportValidate(t *testing.T) {
	report := sampleReport()
	if err := report.Validate(); err != nil {
		t.Fatalf("valid report rejected: %v", err)
	}
	bad := sampleReport()
	bad.SchemaVersion = 2
	if err := bad.Validate(); err == nil {
		t.Fatal("unsupported schema accepted")
	}
	bad = sampleReport()
	bad.Host.Hostname = ""
	if err := bad.Validate(); err == nil {
		t.Fatal("empty hostname accepted")
	}
	bad = sampleReport()
	bad.CollectedAt = "not-a-time"
	if err := bad.Validate(); err == nil {
		t.Fatal("invalid timestamp accepted")
	}
	bad = sampleReport()
	bad.Host.CPU.UsagePercent = 101
	if err := bad.Validate(); err == nil {
		t.Fatal("out-of-range CPU accepted")
	}
}

func TestReportSummary(t *testing.T) {
	summary := sampleReport().Summary()
	if summary.HostID != "host-1" || summary.Hostname != "pve-1" {
		t.Fatalf("identity lost: %#v", summary)
	}
	if summary.DiskPercent != 80 {
		t.Fatalf("max disk usage should be 80, got %v", summary.DiskPercent)
	}
	if summary.PveVMs != 2 || summary.PveContainers != 1 {
		t.Fatalf("PVE counts incorrect: %#v", summary)
	}
	if summary.DockerRunning != 4 || summary.K8sPods != 10 {
		t.Fatalf("platform counts incorrect: %#v", summary)
	}
	if summary.UsersOnline != 3 || summary.UsersStale != 2 {
		t.Fatalf("user counts incorrect: %#v", summary)
	}
	if summary.Virtualization != "physical" {
		t.Fatalf("unexpected virtualization: %q", summary.Virtualization)
	}
}

func TestReportTimestampRoundTrip(t *testing.T) {
	report := sampleReport()
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339, decoded.CollectedAt); err != nil {
		t.Fatalf("timestamp did not survive JSON: %v", err)
	}
}
