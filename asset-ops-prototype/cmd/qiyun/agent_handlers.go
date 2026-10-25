package main

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"qiyun-asset-ops/internal/agents"
	"qiyun-asset-ops/internal/store"
)

const maxAgentReportBytes = 4 << 20

// agentReport accepts reports from remote Linux/PVE agents. It deliberately uses
// a shared bearer token instead of the browser session: agents are headless and
// may live on a different network segment.
func (a *app) agentReport(w http.ResponseWriter, r *http.Request) {
	if a.agentToken == "" {
		writeProblem(w, http.StatusServiceUnavailable, "AGENT_TOKEN_NOT_CONFIGURED",
			"服务端未配置 Agent 令牌；请设置 QIYUN_AGENT_TOKEN，或从 data/agent.token 读取自动生成的令牌。")
		return
	}
	if !agentTokenMatches(r, a.agentToken) {
		writeProblem(w, http.StatusUnauthorized, "AGENT_UNAUTHORIZED", "Agent 令牌无效")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAgentReportBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeProblem(w, http.StatusRequestEntityTooLarge, "AGENT_REPORT_TOO_LARGE", "Agent 上报超过 4 MiB")
			return
		}
		writeProblem(w, http.StatusBadRequest, "AGENT_REPORT_READ_FAILED", "无法读取 Agent 上报内容")
		return
	}
	var report agents.Report
	if err := json.Unmarshal(body, &report); err != nil {
		writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_REPORT", "Agent 上报不是有效的 JSON："+err.Error())
		return
	}
	if err := report.Validate(); err != nil {
		writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_REPORT", err.Error())
		return
	}
	summary, err := a.store.UpsertAgentReport(r.Context(), report, body)
	if err != nil {
		serverError(w, err)
		return
	}
	a.store.Audit(r.Context(), "agent.report", "agent", report.HostID, "success", map[string]any{
		"hostname":  report.Host.Hostname,
		"pve_vms":   summary.PveVMs,
		"docker":    summary.DockerRunning,
		"k8s_nodes": summary.K8sNodes,
	})
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok":          true,
		"host_id":     summary.HostID,
		"received_at": summary.ReceivedAt,
	})
}

func (a *app) agentHosts(w http.ResponseWriter, r *http.Request) {
	query := store.AgentHostQuery{Page: 1, PageSize: 50}
	if raw := strings.TrimSpace(r.URL.Query().Get("page")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_QUERY", "page 必须是正整数")
			return
		}
		query.Page = value
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("page_size")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 200 {
			writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_QUERY", "page_size 必须在 1 到 200 之间")
			return
		}
		query.PageSize = value
	}
	query.Search = strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query.Search) > 200 {
		writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_QUERY", "搜索条件过长")
		return
	}
	query.Status = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	switch query.Status {
	case "", "fresh", "delayed", "stale":
	default:
		writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_QUERY", "status 必须是 fresh/delayed/stale")
		return
	}
	query.Platform = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("platform")))
	switch query.Platform {
	case "", "pve", "docker", "k8s":
	default:
		writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_QUERY", "platform 必须是 pve/docker/k8s")
		return
	}
	query.Sort = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sort")))
	switch query.Sort {
	case "", "freshness", "name", "cpu", "memory", "disk", "age":
	default:
		writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_QUERY", "sort 必须是 freshness/name/cpu/memory/disk/age")
		return
	}
	result, err := a.store.ListAgentHostsPage(r.Context(), query)
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

var agentHostIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func normalizeAgentTags(tags []string) ([]string, error) {
	if len(tags) > 20 {
		return nil, errors.New("标签最多 20 个")
	}
	result := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, raw := range tags {
		tag := strings.TrimSpace(raw)
		if tag == "" || seen[tag] {
			continue
		}
		if len([]rune(tag)) > 40 {
			return nil, errors.New("单个标签最多 40 个字符")
		}
		seen[tag] = true
		result = append(result, tag)
	}
	return result, nil
}

func validateAgentMetadata(displayName, note string) (string, string, error) {
	displayName = strings.TrimSpace(displayName)
	note = strings.TrimSpace(note)
	if len([]rune(displayName)) > 120 {
		return "", "", errors.New("显示名最多 120 个字符")
	}
	if len([]rune(note)) > 2000 {
		return "", "", errors.New("备注最多 2000 个字符")
	}
	return displayName, note, nil
}

func (a *app) agentCreateHost(w http.ResponseWriter, r *http.Request) {
	var input struct {
		HostID      string   `json:"host_id"`
		Hostname    string   `json:"hostname"`
		DisplayName string   `json:"display_name"`
		Note        string   `json:"note"`
		Tags        []string `json:"tags"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&input); err != nil {
		writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_BODY", "登记内容不是有效 JSON")
		return
	}
	input.HostID = strings.TrimSpace(input.HostID)
	input.Hostname = strings.TrimSpace(input.Hostname)
	if !agentHostIDPattern.MatchString(input.HostID) {
		writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_HOST_ID", "Host ID 只能包含字母、数字、点、下划线、冒号、连字符，最长 128")
		return
	}
	if input.Hostname == "" || len([]rune(input.Hostname)) > 255 {
		writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_HOSTNAME", "主机名不能为空，且最多 255 个字符")
		return
	}
	displayName, note, err := validateAgentMetadata(input.DisplayName, input.Note)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_METADATA", err.Error())
		return
	}
	tags, err := normalizeAgentTags(input.Tags)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_TAGS", err.Error())
		return
	}
	summary, err := a.store.CreateAgentHost(r.Context(), input.HostID, input.Hostname, displayName, note, tags)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeProblem(w, http.StatusConflict, "AGENT_HOST_EXISTS", "该 Host ID 已存在")
			return
		}
		serverError(w, err)
		return
	}
	a.store.Audit(r.Context(), "agent.create", "agent", input.HostID, "success", map[string]any{"hostname": input.Hostname})
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "summary": summary})
}

func (a *app) agentUpdateHost(w http.ResponseWriter, r *http.Request) {
	hostID := strings.TrimSpace(r.PathValue("id"))
	if !agentHostIDPattern.MatchString(hostID) {
		writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_HOST_ID", "Host ID 无效")
		return
	}
	var input struct {
		DisplayName *string   `json:"display_name"`
		Note        *string   `json:"note"`
		Tags        *[]string `json:"tags"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&input); err != nil {
		writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_BODY", "更新内容不是有效 JSON")
		return
	}
	current, _, err := a.store.GetAgentHost(r.Context(), hostID)
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, http.StatusNotFound, "AGENT_HOST_NOT_FOUND", "Agent 节点不存在")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	displayName := current.DisplayName
	note := current.Note
	tags := current.Tags
	if input.DisplayName != nil {
		displayName = *input.DisplayName
	}
	if input.Note != nil {
		note = *input.Note
	}
	if input.Tags != nil {
		tags = *input.Tags
	}
	displayName, note, err = validateAgentMetadata(displayName, note)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_METADATA", err.Error())
		return
	}
	tags, err = normalizeAgentTags(tags)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_TAGS", err.Error())
		return
	}
	updated, err := a.store.UpdateAgentHostMetadata(r.Context(), hostID, displayName, note, tags)
	if err != nil {
		serverError(w, err)
		return
	}
	if !updated {
		writeProblem(w, http.StatusNotFound, "AGENT_HOST_NOT_FOUND", "Agent 节点不存在")
		return
	}
	a.store.Audit(r.Context(), "agent.update", "agent", hostID, "success", map[string]any{"display_name": displayName})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *app) agentDeleteHost(w http.ResponseWriter, r *http.Request) {
	hostID := strings.TrimSpace(r.PathValue("id"))
	if !agentHostIDPattern.MatchString(hostID) {
		writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_HOST_ID", "Host ID 无效")
		return
	}
	deleted, err := a.store.DeleteAgentHost(r.Context(), hostID)
	if err != nil {
		serverError(w, err)
		return
	}
	if !deleted {
		writeProblem(w, http.StatusNotFound, "AGENT_HOST_NOT_FOUND", "Agent 节点不存在")
		return
	}
	a.store.Audit(r.Context(), "agent.delete", "agent", hostID, "success", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *app) agentBulkDeleteHosts(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&input); err != nil {
		writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_BODY", "删除内容不是有效 JSON")
		return
	}
	if len(input.IDs) == 0 {
		writeProblem(w, http.StatusBadRequest, "AGENT_IDS_REQUIRED", "至少选择一个节点")
		return
	}
	if len(input.IDs) > 1000 {
		writeProblem(w, http.StatusBadRequest, "AGENT_TOO_MANY_IDS", "单次最多删除 1000 个节点")
		return
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(input.IDs))
	for _, id := range input.IDs {
		id = strings.TrimSpace(id)
		if !agentHostIDPattern.MatchString(id) {
			writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_HOST_ID", "包含无效的 Host ID")
			return
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	deleted, err := a.store.DeleteAgentHosts(r.Context(), ids)
	if err != nil {
		serverError(w, err)
		return
	}
	a.store.Audit(r.Context(), "agent.bulk_delete", "agent", "", "success", map[string]any{"deleted": deleted, "requested": len(ids)})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": deleted})
}

func (a *app) agentPurgeHosts(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OlderThanDays int  `json:"older_than_days"`
		OnlyStale     bool `json:"only_stale"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&input); err != nil {
		writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_BODY", "清理内容不是有效 JSON")
		return
	}
	var cutoff string
	if input.OnlyStale {
		cutoff = time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
	} else {
		if input.OlderThanDays < 1 || input.OlderThanDays > 3650 {
			writeProblem(w, http.StatusBadRequest, "AGENT_INVALID_RETENTION", "older_than_days 必须在 1 到 3650 之间")
			return
		}
		cutoff = time.Now().UTC().AddDate(0, 0, -input.OlderThanDays).Format(time.RFC3339)
	}
	deleted, err := a.store.PurgeAgentHosts(r.Context(), cutoff)
	if err != nil {
		serverError(w, err)
		return
	}
	a.store.Audit(r.Context(), "agent.purge", "agent", "", "success", map[string]any{"older_than_days": input.OlderThanDays, "only_stale": input.OnlyStale, "deleted": deleted})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": deleted, "cutoff": cutoff})
}

func (a *app) agentHost(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("id")
	if strings.TrimSpace(hostID) == "" {
		writeProblem(w, http.StatusBadRequest, "AGENT_HOST_REQUIRED", "缺少 Agent 主机 ID")
		return
	}
	summary, payload, err := a.store.GetAgentHost(r.Context(), hostID)
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, http.StatusNotFound, "AGENT_HOST_NOT_FOUND", "Agent 主机不存在")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"summary": summary, "payload": payload})
}

// agentEnrollment gives an authenticated administrator the exact values needed
// to install the agent: the platform URL, the write-only ingestion token, and a
// download path. The token is deliberately write-only (it cannot read assets),
// and the response is never cached.
func (a *app) agentEnrollment(w http.ResponseWriter, r *http.Request) {
	if a.agentToken == "" {
		writeProblem(w, http.StatusServiceUnavailable, "AGENT_TOKEN_NOT_CONFIGURED",
			"服务端未配置 Agent 令牌；请设置 QIYUN_AGENT_TOKEN，或重启服务自动生成 data/agent.token。")
		return
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https") {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = "127.0.0.1:8765"
	}
	hostOnly := host
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		hostOnly = parsed
	}
	downloadURL := ""
	if info, err := os.Stat(filepath.Join(a.webRoot, "downloads", "qiyun-agent-linux-amd64.zip")); err == nil && !info.IsDir() {
		downloadURL = "/downloads/qiyun-agent-linux-amd64.zip"
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"server_url":   scheme + "://" + host,
		"token":        a.agentToken,
		"token_file":   "data/agent.token",
		"download_url": downloadURL,
		"platform":     "linux-amd64",
		"local_only":   isLoopbackHost(hostOnly),
	})
}

func agentTokenMatches(r *http.Request, expected string) bool {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}
