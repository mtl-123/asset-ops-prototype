// Package agents defines the inventory report sent by the Linux/PVE agent and
// the compact summary shown on the web console. The full report is kept as raw
// JSON so adding platform-specific fields does not require a database migration.
package agents

import (
	"encoding/json"
	"fmt"
	"time"
)

type Report struct {
	SchemaVersion int       `json:"schema_version"`
	AgentVersion  string    `json:"agent_version"`
	HostID        string    `json:"host_id"`
	CollectedAt   string    `json:"collected_at"`
	Host          Host      `json:"host"`
	Users         Users     `json:"users"`
	Platforms     Platforms `json:"platforms"`
}

type Host struct {
	Hostname       string `json:"hostname"`
	OSName         string `json:"os_name"`
	OSVersion      string `json:"os_version"`
	KernelVersion  string `json:"kernel_version"`
	UptimeSeconds  int64  `json:"uptime_seconds"`
	Virtualization struct {
		IsVirtualMachine bool   `json:"is_virtual_machine"`
		Hypervisor       string `json:"hypervisor"`
		Container        string `json:"container"`
	} `json:"virtualization"`
	CPU    CPU    `json:"cpu"`
	Memory Memory `json:"memory"`
	Disks  []Disk `json:"disks"`
}

type CPU struct {
	UsagePercent  float64 `json:"usage_percent"`
	PhysicalCores int     `json:"physical_cores"`
	LogicalCores  int     `json:"logical_cores"`
}

type Memory struct {
	TotalBytes   int64   `json:"total_bytes"`
	UsedBytes    int64   `json:"used_bytes"`
	UsagePercent float64 `json:"usage_percent"`
}

type Disk struct {
	Mount        string  `json:"mount"`
	TotalBytes   int64   `json:"total_bytes"`
	UsedBytes    int64   `json:"used_bytes"`
	UsagePercent float64 `json:"usage_percent"`
}

type Users struct {
	LoggedInCount    int  `json:"logged_in_count"`
	SessionCount     int  `json:"session_count"`
	LocalUserCount   int  `json:"local_user_count"`
	StaleDays        int  `json:"stale_days"`
	StaleCount       int  `json:"stale_count"`
	LastlogAvailable bool `json:"lastlog_available"`
}

type Platforms struct {
	Pve        Pve        `json:"pve"`
	Docker     Docker     `json:"docker"`
	Kubernetes Kubernetes `json:"kubernetes"`
}

type Pve struct {
	Available  bool              `json:"available"`
	Node       string            `json:"node"`
	Version    *string           `json:"version"`
	Error      *string           `json:"error"`
	VMs        []json.RawMessage `json:"vms"`
	Containers []json.RawMessage `json:"containers"`
}

type Docker struct {
	Available         bool    `json:"available"`
	Version           *string `json:"version"`
	Error             *string `json:"error"`
	ContainersTotal   int     `json:"containers_total"`
	ContainersRunning int     `json:"containers_running"`
}

type Kubernetes struct {
	Available  bool    `json:"available"`
	Context    *string `json:"context"`
	Version    *string `json:"version"`
	Error      *string `json:"error"`
	NodeCount  int     `json:"node_count"`
	NodesReady int     `json:"nodes_ready"`
	PodCount   int     `json:"pod_count"`
}

// Summary is what the host list needs. It is intentionally small so the list
// endpoint does not ship every VM/container/pod object to the browser.
type Summary struct {
	HostID           string   `json:"host_id"`
	Hostname         string   `json:"hostname"`
	AgentVersion     string   `json:"agent_version"`
	OSName           string   `json:"os_name"`
	CollectedAt      string   `json:"collected_at"`
	ReceivedAt       string   `json:"received_at"`
	CPUPercent       float64  `json:"cpu_percent"`
	MemoryPercent    float64  `json:"memory_percent"`
	DiskPercent      float64  `json:"disk_percent"`
	UptimeSeconds    int64    `json:"uptime_seconds"`
	UsersOnline      int      `json:"users_online"`
	UsersStale       int      `json:"users_stale"`
	PveAvailable     bool     `json:"pve_available"`
	PveVMs           int      `json:"pve_vms"`
	PveContainers    int      `json:"pve_containers"`
	DockerAvailable  bool     `json:"docker_available"`
	DockerRunning    int      `json:"docker_running"`
	DockerTotal      int      `json:"docker_total"`
	K8sAvailable     bool     `json:"k8s_available"`
	K8sNodes         int      `json:"k8s_nodes"`
	K8sPods          int      `json:"k8s_pods"`
	Virtualization   string   `json:"virtualization"`
	HypervisorDetail string   `json:"hypervisor_detail"`
	DisplayName      string   `json:"display_name,omitempty"`
	Note             string   `json:"note,omitempty"`
	Tags             []string `json:"tags,omitempty"`
}

func (r Report) Validate() error {
	if r.SchemaVersion != 1 {
		return fmt.Errorf("unsupported schema_version %d", r.SchemaVersion)
	}
	if r.HostID == "" || len(r.HostID) > 128 {
		return fmt.Errorf("host_id is required and must be at most 128 characters")
	}
	if r.Host.Hostname == "" || len(r.Host.Hostname) > 255 {
		return fmt.Errorf("host.hostname is required and must be at most 255 characters")
	}
	if len(r.AgentVersion) > 64 {
		return fmt.Errorf("agent_version is too long")
	}
	if _, err := time.Parse(time.RFC3339, r.CollectedAt); err != nil {
		return fmt.Errorf("collected_at must be RFC3339: %w", err)
	}
	if r.Host.UptimeSeconds < 0 || r.Host.CPU.LogicalCores < 0 || r.Host.CPU.PhysicalCores < 0 {
		return fmt.Errorf("host counters cannot be negative")
	}
	if r.Host.CPU.UsagePercent < 0 || r.Host.CPU.UsagePercent > 100 {
		return fmt.Errorf("cpu usage must be between 0 and 100")
	}
	if r.Host.Memory.UsagePercent < 0 || r.Host.Memory.UsagePercent > 100 {
		return fmt.Errorf("memory usage must be between 0 and 100")
	}
	if r.Users.LoggedInCount < 0 || r.Users.StaleCount < 0 || r.Users.SessionCount < 0 {
		return fmt.Errorf("user counters cannot be negative")
	}
	if r.Platforms.Docker.ContainersTotal < 0 || r.Platforms.Docker.ContainersRunning < 0 {
		return fmt.Errorf("docker counters cannot be negative")
	}
	if r.Platforms.Kubernetes.NodeCount < 0 || r.Platforms.Kubernetes.PodCount < 0 {
		return fmt.Errorf("kubernetes counters cannot be negative")
	}
	return nil
}

func (r Report) Summary() Summary {
	diskPercent := 0.0
	for _, disk := range r.Host.Disks {
		if disk.UsagePercent > diskPercent {
			diskPercent = disk.UsagePercent
		}
	}
	virtualization := "physical"
	if r.Host.Virtualization.IsVirtualMachine {
		virtualization = "vm"
	}
	if r.Host.Virtualization.Container != "" && r.Host.Virtualization.Container != "none" {
		virtualization = "container"
	}
	return Summary{
		HostID:           r.HostID,
		Hostname:         r.Host.Hostname,
		AgentVersion:     r.AgentVersion,
		OSName:           r.Host.OSName,
		CollectedAt:      r.CollectedAt,
		CPUPercent:       r.Host.CPU.UsagePercent,
		MemoryPercent:    r.Host.Memory.UsagePercent,
		DiskPercent:      diskPercent,
		UptimeSeconds:    r.Host.UptimeSeconds,
		UsersOnline:      r.Users.LoggedInCount,
		UsersStale:       r.Users.StaleCount,
		PveAvailable:     r.Platforms.Pve.Available,
		PveVMs:           len(r.Platforms.Pve.VMs),
		PveContainers:    len(r.Platforms.Pve.Containers),
		DockerAvailable:  r.Platforms.Docker.Available,
		DockerRunning:    r.Platforms.Docker.ContainersRunning,
		DockerTotal:      r.Platforms.Docker.ContainersTotal,
		K8sAvailable:     r.Platforms.Kubernetes.Available,
		K8sNodes:         r.Platforms.Kubernetes.NodeCount,
		K8sPods:          r.Platforms.Kubernetes.PodCount,
		Virtualization:   virtualization,
		HypervisorDetail: r.Host.Virtualization.Hypervisor,
	}
}
