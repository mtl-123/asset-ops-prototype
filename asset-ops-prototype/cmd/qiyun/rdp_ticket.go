package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"qiyun-asset-ops/internal/auth"
	"qiyun-asset-ops/internal/rdp"
	"qiyun-asset-ops/internal/store"
)

const rdpTicketTTL = 90 * time.Second

func (a *app) rdpConnectorInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"connector_url": env("QIYUN_CONNECTOR_URL", "http://127.0.0.1:8766"),
		"download_url":  env("QIYUN_CONNECTOR_DOWNLOAD", "/downloads/QiyunConnector.zip"),
		"web_rdp":       a.guac != nil,
	})
}

// webRDP creates or updates a Guacamole RDP connection and returns the opaque
// browser client URL. Guacamole itself terminates RDP; no local component is
// installed on the user's PC.
func (a *app) webRDP(w http.ResponseWriter, r *http.Request) {
	if a.guac == nil {
		writeProblem(w, http.StatusNotImplemented, "WEB_RDP_NOT_CONFIGURED", "未配置 Guacamole 网关")
		return
	}
	assetID := r.PathValue("id")
	if !a.assetConnectable(r, assetID, "rdp.connect") {
		writeProblem(w, http.StatusNotFound, "NOT_FOUND", "资产不存在或无权连接")
		return
	}
	asset, credential, err := a.store.CredentialForAsset(r.Context(), assetID)
	if err != nil {
		writeProblem(w, http.StatusNotFound, "RDP_ASSET_NOT_FOUND", "资产不存在或已删除")
		return
	}
	if !asset.IsWindows() {
		writeProblem(w, http.StatusBadRequest, "RDP_ASSET_NOT_WINDOWS", "该资产不是 Windows 主机")
		return
	}
	var in rdp.LaunchInput
	if r.Body != nil {
		if !decodeJSON(w, r, &in) {
			return
		}
	}
	if credential.AuthType != "password" || strings.TrimSpace(credential.Secret) == "" {
		writeProblem(w, http.StatusConflict, "RDP_PASSWORD_REQUIRED", "该资产没有可用的 RDP 密码凭据")
		return
	}
	port := asset.RDPPort
	if in.Port > 0 {
		port = in.Port
	}
	if port == 0 {
		port = 3389
	}
	parameters := map[string]string{
		"hostname":    asset.Address,
		"port":        strconv.Itoa(port),
		"username":    credential.Username,
		"password":    credential.Secret,
		"security":    env("QIYUN_GUACAMOLE_RDP_SECURITY", "nla"),
		"ignore-cert": "true",
	}
	if domain := strings.TrimSpace(os.Getenv("QIYUN_GUACAMOLE_DOMAIN")); domain != "" {
		parameters["domain"] = domain
	}
	if secLevel := strings.TrimSpace(os.Getenv("QIYUN_GUACAMOLE_TLS_SECLEVEL")); secLevel != "" {
		parameters["tls-seclevel"] = secLevel
	}
	clientURL, err := a.guac.EnsureConnection(r.Context(), asset.ID, parameters)
	if err != nil {
		slog.Warn("guacamole connection failed", "asset_id", asset.ID, "error", err)
		a.store.Audit(r.Context(), "asset.rdp.web", "asset", asset.ID, "failed", nil)
		writeProblem(w, http.StatusBadGateway, "WEB_RDP_FAILED", "无法创建 Guacamole 连接，请检查网关配置与网络")
		return
	}
	a.store.Audit(r.Context(), "asset.rdp.web", "asset", asset.ID, "success", map[string]any{"target": asset.Address})
	writeJSON(w, http.StatusOK, map[string]any{"url": clientURL, "mode": "guacamole"})
}

// createRDPTicket stores a short-lived, single-use handle for the user's local
// connector. The ticket carries only asset ID and resource options; credentials
// are resolved when the connector redeems it.
func (a *app) createRDPTicket(w http.ResponseWriter, r *http.Request) {
	assetID := r.PathValue("id")
	if !a.assetConnectable(r, assetID, "rdp.connect") {
		writeProblem(w, http.StatusNotFound, "NOT_FOUND", "资产不存在或无权连接")
		return
	}
	asset, err := a.store.GetAsset(r.Context(), assetID)
	if err != nil {
		notFoundOrServer(w, err)
		return
	}
	if !asset.IsWindows() {
		writeProblem(w, http.StatusBadRequest, "RDP_ASSET_NOT_WINDOWS", "该资产不是 Windows 主机")
		return
	}
	var in rdp.LaunchInput
	if !decodeJSON(w, r, &in) {
		return
	}
	port := asset.RDPPort
	if in.Port > 0 {
		port = in.Port
	}
	if port == 0 {
		port = 3389
	}
	// Validate target/port/resource options without storing a credential. The
	// placeholder username passes the "password requires username" check.
	check := rdp.Profile{
		Address: asset.Address, Port: port, Username: "ticket",
		RedirectClipboard: in.RedirectClipboard, RedirectDrives: in.RedirectDrives,
		DriveMode: in.DriveMode, DriveList: in.DriveList,
		Fullscreen: in.Fullscreen, MultiMonitor: in.MultiMonitor,
	}
	if strings.TrimSpace(check.DriveMode) == "" {
		check.DriveMode = "all"
	}
	if err := rdp.Validate(check); err != nil {
		var rdpErr *rdp.RDPError
		if errors.As(err, &rdpErr) {
			writeProblem(w, rdpErr.Status, rdpErr.Code, rdpErr.Message)
			return
		}
		writeProblem(w, http.StatusBadRequest, "RDP_INVALID_TARGET", err.Error())
		return
	}
	ticket, err := auth.NewToken()
	if err != nil {
		serverError(w, err)
		return
	}
	options, err := json.Marshal(in)
	if err != nil {
		serverError(w, err)
		return
	}
	expires := time.Now().UTC().Add(rdpTicketTTL)
	if err := a.store.CreateRDPTicket(r.Context(), auth.HashToken(ticket), principalID(r), asset.ID, string(options), expires); err != nil {
		serverError(w, err)
		return
	}
	a.store.Audit(r.Context(), "rdp.ticket.create", "asset", asset.ID, "success", map[string]any{"expires_at": expires})
	writeJSON(w, http.StatusOK, map[string]any{
		"ticket":             ticket,
		"expires_at":         expires,
		"connector_url":      env("QIYUN_CONNECTOR_URL", "http://127.0.0.1:8766"),
		"connector_download": env("QIYUN_CONNECTOR_DOWNLOAD", "/downloads/QiyunConnector.zip"),
	})
}

func principalID(r *http.Request) string {
	if principal, ok := auth.PrincipalFrom(r.Context()); ok {
		return principal.UserID
	}
	return ""
}

// redeemRDPTicket is intentionally public: the ticket is the bearer credential.
// It is single-use, expires quickly, and returns a profile over TLS. The browser
// never sees the profile or the Windows password.
func (a *app) redeemRDPTicket(w http.ResponseWriter, r *http.Request) {
	ticketValue := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if ticketValue == "" {
		writeProblem(w, http.StatusUnauthorized, "RDP_TICKET_INVALID", "连接票据无效或已过期")
		return
	}
	ticket, err := a.store.ConsumeRDPTicket(r.Context(), auth.HashToken(ticketValue))
	if err != nil {
		if errors.Is(err, store.ErrTicketInvalid) {
			writeProblem(w, http.StatusUnauthorized, "RDP_TICKET_INVALID", "连接票据无效或已过期")
			return
		}
		serverError(w, err)
		return
	}
	asset, credential, err := a.store.CredentialForAsset(r.Context(), ticket.AssetID)
	if err != nil {
		writeProblem(w, http.StatusNotFound, "RDP_ASSET_NOT_FOUND", "资产不存在或已删除")
		return
	}
	if !asset.IsWindows() {
		writeProblem(w, http.StatusBadRequest, "RDP_ASSET_NOT_WINDOWS", "该资产不是 Windows 主机")
		return
	}
	var options rdp.LaunchInput
	if err := json.Unmarshal([]byte(ticket.Options), &options); err != nil {
		serverError(w, err)
		return
	}
	port := asset.RDPPort
	if options.Port > 0 {
		port = options.Port
	}
	if port == 0 {
		port = 3389
	}
	profile := rdp.Profile{
		Address: asset.Address, Port: port,
		RedirectClipboard: options.RedirectClipboard, RedirectDrives: options.RedirectDrives,
		DriveMode: options.DriveMode, DriveList: options.DriveList,
		Fullscreen: options.Fullscreen, MultiMonitor: options.MultiMonitor,
	}
	if strings.TrimSpace(profile.DriveMode) == "" {
		profile.DriveMode = "all"
	}
	if !options.PromptCredentials {
		if credential.AuthType != "password" || strings.TrimSpace(credential.Secret) == "" {
			writeProblem(w, http.StatusConflict, "RDP_PASSWORD_REQUIRED", "该资产没有可用的 RDP 密码凭据")
			return
		}
		profile.Username = credential.Username
		profile.Password = credential.Secret
	}
	if err := rdp.Validate(profile); err != nil {
		var rdpErr *rdp.RDPError
		if errors.As(err, &rdpErr) {
			writeProblem(w, rdpErr.Status, rdpErr.Code, rdpErr.Message)
			return
		}
		writeProblem(w, http.StatusBadRequest, "RDP_INVALID_PROFILE", err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, profile)
	a.store.Audit(store.WithActor(r.Context(), ticket.UserID), "rdp.ticket.redeem", "asset", asset.ID, "success", map[string]any{"target": asset.Address})
}
