package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"qiyun-asset-ops/internal/agents"
)

const agentHostsDDL = `CREATE TABLE IF NOT EXISTS agent_hosts (
 host_id TEXT PRIMARY KEY,
 hostname TEXT NOT NULL,
 agent_version TEXT NOT NULL DEFAULT '',
 os_name TEXT NOT NULL DEFAULT '',
 collected_at TEXT NOT NULL,
 received_at TEXT NOT NULL,
 cpu_percent REAL NOT NULL DEFAULT 0,
 memory_percent REAL NOT NULL DEFAULT 0,
 disk_percent REAL NOT NULL DEFAULT 0,
 uptime_seconds INTEGER NOT NULL DEFAULT 0,
 users_online INTEGER NOT NULL DEFAULT 0,
 users_stale INTEGER NOT NULL DEFAULT 0,
 pve_available INTEGER NOT NULL DEFAULT 0,
 pve_vms INTEGER NOT NULL DEFAULT 0,
 pve_containers INTEGER NOT NULL DEFAULT 0,
 docker_available INTEGER NOT NULL DEFAULT 0,
 docker_running INTEGER NOT NULL DEFAULT 0,
 docker_total INTEGER NOT NULL DEFAULT 0,
 k8s_available INTEGER NOT NULL DEFAULT 0,
 k8s_nodes INTEGER NOT NULL DEFAULT 0,
 k8s_pods INTEGER NOT NULL DEFAULT 0,
 virtualization TEXT NOT NULL DEFAULT 'physical',
 hypervisor_detail TEXT NOT NULL DEFAULT '',
 display_name TEXT NOT NULL DEFAULT '',
 note TEXT NOT NULL DEFAULT '',
 tags TEXT NOT NULL DEFAULT '[]',
 payload TEXT NOT NULL
)`

func (s *Store) ensureAgentSchema(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, agentHostsDDL); err != nil {
		return err
	}
	// Additive migration for databases created before operator metadata existed.
	for _, column := range []string{
		"display_name TEXT NOT NULL DEFAULT ''",
		"note TEXT NOT NULL DEFAULT ''",
		"tags TEXT NOT NULL DEFAULT '[]'",
	} {
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE agent_hosts ADD COLUMN "+column); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
				continue
			}
			return err
		}
	}
	return nil
}

func (s *Store) UpsertAgentReport(ctx context.Context, report agents.Report, payload []byte) (agents.Summary, error) {
	summary := report.Summary()
	summary.ReceivedAt = time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO agent_hosts (
 host_id, hostname, agent_version, os_name, collected_at, received_at,
 cpu_percent, memory_percent, disk_percent, uptime_seconds, users_online, users_stale,
 pve_available, pve_vms, pve_containers,
 docker_available, docker_running, docker_total,
 k8s_available, k8s_nodes, k8s_pods, virtualization, hypervisor_detail, payload
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(host_id) DO UPDATE SET
 hostname=excluded.hostname,
 agent_version=excluded.agent_version,
 os_name=excluded.os_name,
 collected_at=excluded.collected_at,
 received_at=excluded.received_at,
 cpu_percent=excluded.cpu_percent,
 memory_percent=excluded.memory_percent,
 disk_percent=excluded.disk_percent,
 uptime_seconds=excluded.uptime_seconds,
 users_online=excluded.users_online,
 users_stale=excluded.users_stale,
 pve_available=excluded.pve_available,
 pve_vms=excluded.pve_vms,
 pve_containers=excluded.pve_containers,
 docker_available=excluded.docker_available,
 docker_running=excluded.docker_running,
 docker_total=excluded.docker_total,
 k8s_available=excluded.k8s_available,
 k8s_nodes=excluded.k8s_nodes,
 k8s_pods=excluded.k8s_pods,
 virtualization=excluded.virtualization,
 hypervisor_detail=excluded.hypervisor_detail,
 payload=excluded.payload`,
		summary.HostID,
		summary.Hostname,
		summary.AgentVersion,
		summary.OSName,
		summary.CollectedAt,
		summary.ReceivedAt,
		summary.CPUPercent,
		summary.MemoryPercent,
		summary.DiskPercent,
		summary.UptimeSeconds,
		summary.UsersOnline,
		summary.UsersStale,
		boolInt(summary.PveAvailable),
		summary.PveVMs,
		summary.PveContainers,
		boolInt(summary.DockerAvailable),
		summary.DockerRunning,
		summary.DockerTotal,
		boolInt(summary.K8sAvailable),
		summary.K8sNodes,
		summary.K8sPods,
		summary.Virtualization,
		summary.HypervisorDetail,
		string(payload),
	)
	if err != nil {
		return agents.Summary{}, fmt.Errorf("upsert agent report: %w", err)
	}
	return summary, nil
}

func (s *Store) ListAgentHosts(ctx context.Context) ([]agents.Summary, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT host_id, hostname, agent_version, os_name, collected_at, received_at,
 cpu_percent, memory_percent, disk_percent, uptime_seconds, users_online, users_stale,
 pve_available, pve_vms, pve_containers,
 docker_available, docker_running, docker_total,
 k8s_available, k8s_nodes, k8s_pods, virtualization, hypervisor_detail,
 display_name, note, tags
FROM agent_hosts ORDER BY hostname COLLATE NOCASE, host_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []agents.Summary
	for rows.Next() {
		summary, _, err := scanAgentSummary(rows, false)
		if err != nil {
			return nil, err
		}
		result = append(result, summary)
	}
	return result, rows.Err()
}

func (s *Store) GetAgentHost(ctx context.Context, hostID string) (agents.Summary, json.RawMessage, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT host_id, hostname, agent_version, os_name, collected_at, received_at,
 cpu_percent, memory_percent, disk_percent, uptime_seconds, users_online, users_stale,
 pve_available, pve_vms, pve_containers,
 docker_available, docker_running, docker_total,
 k8s_available, k8s_nodes, k8s_pods, virtualization, hypervisor_detail,
 display_name, note, tags, payload
FROM agent_hosts WHERE host_id=?`, hostID)
	return scanAgentSummary(row, true)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAgentSummary(row rowScanner, withPayload bool) (agents.Summary, json.RawMessage, error) {
	var summary agents.Summary
	var pve, docker, k8s int
	dest := []any{
		&summary.HostID, &summary.Hostname, &summary.AgentVersion, &summary.OSName,
		&summary.CollectedAt, &summary.ReceivedAt,
		&summary.CPUPercent, &summary.MemoryPercent, &summary.DiskPercent,
		&summary.UptimeSeconds, &summary.UsersOnline, &summary.UsersStale,
		&pve, &summary.PveVMs, &summary.PveContainers,
		&docker, &summary.DockerRunning, &summary.DockerTotal,
		&k8s, &summary.K8sNodes, &summary.K8sPods,
		&summary.Virtualization, &summary.HypervisorDetail,
		&summary.DisplayName, &summary.Note,
	}
	var tagsJSON, payload string
	dest = append(dest, &tagsJSON)
	if withPayload {
		dest = append(dest, &payload)
	}
	if err := row.Scan(dest...); err != nil {
		return agents.Summary{}, nil, err
	}
	summary.PveAvailable = pve != 0
	summary.DockerAvailable = docker != 0
	summary.K8sAvailable = k8s != 0
	if strings.TrimSpace(tagsJSON) != "" {
		if err := json.Unmarshal([]byte(tagsJSON), &summary.Tags); err != nil {
			summary.Tags = nil
		}
	}
	if withPayload {
		return summary, json.RawMessage(payload), nil
	}
	return summary, nil, nil
}

// AgentHostQuery is the server-side filter/pagination contract for the fleet
// table. The store keeps the SQL whitelist; callers only pass normalized values.
type AgentHostQuery struct {
	Search   string
	Status   string
	Platform string
	Sort     string
	Page     int
	PageSize int
}

// AgentFleetSummary is computed across the filtered fleet, not just the visible
// page. It lets a 1000-host dashboard show honest totals while paginating rows.
type AgentFleetSummary struct {
	Hosts         int     `json:"hosts"`
	Fresh         int     `json:"fresh"`
	Delayed       int     `json:"delayed"`
	Stale         int     `json:"stale"`
	CPUAvg        float64 `json:"cpu_avg"`
	MemoryAvg     float64 `json:"memory_avg"`
	DiskMax       float64 `json:"disk_max"`
	PveHosts      int     `json:"pve_hosts"`
	PveVMs        int     `json:"pve_vms"`
	PveContainers int     `json:"pve_containers"`
	DockerHosts   int     `json:"docker_hosts"`
	DockerRunning int     `json:"docker_running"`
	DockerTotal   int     `json:"docker_total"`
	K8sHosts      int     `json:"k8s_hosts"`
	K8sNodes      int     `json:"k8s_nodes"`
	K8sPods       int     `json:"k8s_pods"`
	UsersOnline   int     `json:"users_online"`
	UsersStale    int     `json:"users_stale"`
}

type AgentHostPage struct {
	Items       []agents.Summary  `json:"items"`
	Total       int               `json:"total"`
	Page        int               `json:"page"`
	PageSize    int               `json:"page_size"`
	Pages       int               `json:"pages"`
	Summary     AgentFleetSummary `json:"summary"`
	GeneratedAt string            `json:"generated_at"`
}

func (s *Store) ListAgentHostsPage(ctx context.Context, q AgentHostQuery) (AgentHostPage, error) {
	page := q.Page
	if page < 1 {
		page = 1
	}
	pageSize := q.PageSize
	if pageSize < 1 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}

	now := time.Now().UTC()
	freshCutoff := now.Add(-2 * time.Minute).Format(time.RFC3339)
	staleCutoff := now.Add(-10 * time.Minute).Format(time.RFC3339)
	where := make([]string, 0, 4)
	args := make([]any, 0, 8)
	if search := strings.TrimSpace(q.Search); search != "" {
		like := "%" + search + "%"
		where = append(where, "(hostname LIKE ? OR host_id LIKE ? OR os_name LIKE ? OR display_name LIKE ?)")
		args = append(args, like, like, like, like)
	}
	switch q.Status {
	case "fresh":
		where = append(where, "received_at >= ?")
		args = append(args, freshCutoff)
	case "delayed":
		where = append(where, "received_at >= ? AND received_at < ?")
		args = append(args, staleCutoff, freshCutoff)
	case "stale":
		where = append(where, "received_at < ?")
		args = append(args, staleCutoff)
	}
	switch q.Platform {
	case "pve":
		where = append(where, "pve_available = 1")
	case "docker":
		where = append(where, "docker_available = 1")
	case "k8s":
		where = append(where, "k8s_available = 1")
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_hosts"+whereSQL, args...).Scan(&total); err != nil {
		return AgentHostPage{}, fmt.Errorf("count agent hosts: %w", err)
	}
	pages := 0
	if total > 0 {
		pages = (total + pageSize - 1) / pageSize
	}
	if pages > 0 && page > pages {
		page = pages
	}
	offset := (page - 1) * pageSize

	order := "received_at ASC, hostname COLLATE NOCASE"
	switch q.Sort {
	case "name":
		order = "hostname COLLATE NOCASE, host_id"
	case "cpu":
		order = "cpu_percent DESC, hostname COLLATE NOCASE"
	case "memory":
		order = "memory_percent DESC, hostname COLLATE NOCASE"
	case "disk":
		order = "disk_percent DESC, hostname COLLATE NOCASE"
	case "age":
		order = "received_at ASC, hostname COLLATE NOCASE"
	}
	query := `SELECT host_id, hostname, agent_version, os_name, collected_at, received_at,
	 cpu_percent, memory_percent, disk_percent, uptime_seconds, users_online, users_stale,
	 pve_available, pve_vms, pve_containers,
	 docker_available, docker_running, docker_total,
	 k8s_available, k8s_nodes, k8s_pods, virtualization, hypervisor_detail,
	 display_name, note, tags
	 FROM agent_hosts` + whereSQL + " ORDER BY " + order + " LIMIT ? OFFSET ?"
	rows, err := s.db.QueryContext(ctx, query, append(args, pageSize, offset)...)
	if err != nil {
		return AgentHostPage{}, fmt.Errorf("list agent hosts: %w", err)
	}
	defer rows.Close()
	items := make([]agents.Summary, 0, pageSize)
	for rows.Next() {
		summary, _, err := scanAgentSummary(rows, false)
		if err != nil {
			return AgentHostPage{}, err
		}
		items = append(items, summary)
	}
	if err := rows.Err(); err != nil {
		return AgentHostPage{}, err
	}

	summarySQL := `SELECT COUNT(*),
	 COALESCE(SUM(CASE WHEN received_at >= ? THEN 1 ELSE 0 END), 0),
	 COALESCE(SUM(CASE WHEN received_at >= ? AND received_at < ? THEN 1 ELSE 0 END), 0),
	 COALESCE(SUM(CASE WHEN received_at < ? THEN 1 ELSE 0 END), 0),
	 COALESCE(AVG(cpu_percent), 0), COALESCE(AVG(memory_percent), 0), COALESCE(MAX(disk_percent), 0),
	 COALESCE(SUM(CASE WHEN pve_available = 1 THEN 1 ELSE 0 END), 0), COALESCE(SUM(pve_vms), 0), COALESCE(SUM(pve_containers), 0),
	 COALESCE(SUM(CASE WHEN docker_available = 1 THEN 1 ELSE 0 END), 0), COALESCE(SUM(docker_running), 0), COALESCE(SUM(docker_total), 0),
	 COALESCE(SUM(CASE WHEN k8s_available = 1 THEN 1 ELSE 0 END), 0), COALESCE(SUM(k8s_nodes), 0), COALESCE(SUM(k8s_pods), 0),
	 COALESCE(SUM(users_online), 0), COALESCE(SUM(users_stale), 0)
	 FROM agent_hosts` + whereSQL
	summaryArgs := []any{freshCutoff, staleCutoff, freshCutoff, staleCutoff}
	summaryArgs = append(summaryArgs, args...)
	var fleet AgentFleetSummary
	if err := s.db.QueryRowContext(ctx, summarySQL, summaryArgs...).Scan(
		&fleet.Hosts, &fleet.Fresh, &fleet.Delayed, &fleet.Stale,
		&fleet.CPUAvg, &fleet.MemoryAvg, &fleet.DiskMax,
		&fleet.PveHosts, &fleet.PveVMs, &fleet.PveContainers,
		&fleet.DockerHosts, &fleet.DockerRunning, &fleet.DockerTotal,
		&fleet.K8sHosts, &fleet.K8sNodes, &fleet.K8sPods,
		&fleet.UsersOnline, &fleet.UsersStale,
	); err != nil {
		return AgentHostPage{}, fmt.Errorf("summarize agent hosts: %w", err)
	}
	return AgentHostPage{Items: items, Total: total, Page: page, PageSize: pageSize, Pages: pages, Summary: fleet, GeneratedAt: now.Format(time.RFC3339)}, nil
}

func (s *Store) CreateAgentHost(ctx context.Context, hostID, hostname, displayName, note string, tags []string) (agents.Summary, error) {
	if tags == nil {
		tags = []string{}
	}
	rawTags, err := json.Marshal(tags)
	if err != nil {
		return agents.Summary{}, err
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO agent_hosts (host_id, hostname, collected_at, received_at, payload, display_name, note, tags)
VALUES (?, ?, '', '', '{}', ?, ?, ?)`, hostID, hostname, displayName, note, string(rawTags)); err != nil {
		return agents.Summary{}, fmt.Errorf("create agent host: %w", err)
	}
	summary, _, err := s.GetAgentHost(ctx, hostID)
	if err != nil {
		return agents.Summary{}, err
	}
	return summary, nil
}

func (s *Store) UpdateAgentHostMetadata(ctx context.Context, hostID, displayName, note string, tags []string) (bool, error) {
	if tags == nil {
		tags = []string{}
	}
	rawTags, err := json.Marshal(tags)
	if err != nil {
		return false, err
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE agent_hosts SET display_name=?, note=?, tags=? WHERE host_id=?`, displayName, note, string(rawTags), hostID)
	if err != nil {
		return false, fmt.Errorf("update agent host: %w", err)
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (s *Store) DeleteAgentHost(ctx context.Context, hostID string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM agent_hosts WHERE host_id=?`, hostID)
	if err != nil {
		return false, fmt.Errorf("delete agent host: %w", err)
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (s *Store) DeleteAgentHosts(ctx context.Context, hostIDs []string) (int, error) {
	if len(hostIDs) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(hostIDs))
	args := make([]any, len(hostIDs))
	for i, id := range hostIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM agent_hosts WHERE host_id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return 0, fmt.Errorf("delete agent hosts: %w", err)
	}
	affected, err := result.RowsAffected()
	return int(affected), err
}

func (s *Store) PurgeAgentHosts(ctx context.Context, cutoff string) (int, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM agent_hosts WHERE received_at <> '' AND received_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("purge agent hosts: %w", err)
	}
	affected, err := result.RowsAffected()
	return int(affected), err
}
