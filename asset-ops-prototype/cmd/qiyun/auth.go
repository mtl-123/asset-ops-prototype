package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"qiyun-asset-ops/internal/auth"
	"qiyun-asset-ops/internal/store"
)

const sessionCookieName = "qiyun_session"

type loginAttempt struct {
	count        int
	windowStart  time.Time
	blockedUntil time.Time
}

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: map[string]loginAttempt{}}
}

func (l *loginLimiter) allowed(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt, ok := l.attempts[key]
	if !ok {
		return true
	}
	if attempt.blockedUntil.After(time.Now()) {
		return false
	}
	if time.Since(attempt.windowStart) > 5*time.Minute {
		delete(l.attempts, key)
		return true
	}
	return attempt.count < 5
}

func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt := l.attempts[key]
	if attempt.windowStart.IsZero() || time.Since(attempt.windowStart) > 5*time.Minute {
		attempt = loginAttempt{windowStart: time.Now()}
	}
	attempt.count++
	if attempt.count >= 5 {
		attempt.blockedUntil = time.Now().Add(5 * time.Minute)
	}
	l.attempts[key] = attempt
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func bearerToken(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

// auth authenticates a request and attaches the principal to its context. It
// deliberately does not authorize a specific action: routes use permit() and
// handlers apply asset-level grants.
func (a *app) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.authEnabled {
			if a.token == "" {
				next.ServeHTTP(w, r)
				return
			}
			if subtle.ConstantTimeCompare([]byte(bearerToken(r)), []byte(a.token)) == 1 {
				ctx := auth.WithPrincipal(r.Context(), auth.Principal{UserID: "api-token", Username: "api-token", DisplayName: "API token", Role: auth.RoleAdmin})
				ctx = store.WithActor(ctx, "api-token")
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			writeProblem(w, http.StatusUnauthorized, "UNAUTHORIZED", "需要有效的管理令牌")
			return
		}
		if a.token != "" && subtle.ConstantTimeCompare([]byte(bearerToken(r)), []byte(a.token)) == 1 {
			ctx := auth.WithPrincipal(r.Context(), auth.Principal{UserID: "api-token", Username: "api-token", DisplayName: "API token", Role: auth.RoleAdmin})
			ctx = store.WithActor(ctx, "api-token")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		session, ok := a.sessionFromRequest(r)
		if !ok {
			writeProblem(w, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录")
			return
		}
		if !safeMethod(r.Method) && !validCSRF(r, session.CSRF) {
			writeProblem(w, http.StatusForbidden, "CSRF_INVALID", "请求缺少有效的 CSRF 令牌")
			return
		}
		ctx := auth.WithPrincipal(r.Context(), auth.Principal{
			UserID: session.UserID, Username: session.Username,
			DisplayName: session.DisplayName, Role: session.Role,
		})
		ctx = auth.WithCSRF(ctx, session.CSRF)
		ctx = store.WithActor(ctx, session.UserID)
		a.store.TouchAuthSession(ctx, session.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *app) permit(permission string, next http.Handler) http.Handler {
	return a.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFrom(r.Context())
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		if !auth.HasPermission(principal.Role, permission) {
			writeProblem(w, http.StatusForbidden, "FORBIDDEN", "当前账号没有该操作权限")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (a *app) adminOnly(next http.Handler) http.Handler {
	return a.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFrom(r.Context())
		if ok && principal.Role != auth.RoleAdmin {
			writeProblem(w, http.StatusForbidden, "ADMIN_REQUIRED", "只有管理员可以执行该操作")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (a *app) sessionFromRequest(r *http.Request) (store.AuthSession, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return store.AuthSession{}, false
	}
	session, err := a.store.AuthSessionByToken(r.Context(), auth.HashToken(cookie.Value))
	if err != nil {
		return store.AuthSession{}, false
	}
	return session, true
}

func validCSRF(r *http.Request, expected string) bool {
	provided := r.Header.Get("X-CSRF-Token")
	if provided == "" || expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func isSecureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: value, Path: "/", HttpOnly: true,
		Secure: isSecureRequest(r), SameSite: http.SameSiteLaxMode,
		Expires: expires, MaxAge: int(time.Until(expires).Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true,
		Secure: isSecureRequest(r), SameSite: http.SameSiteLaxMode,
		MaxAge: -1, Expires: time.Unix(1, 0),
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (a *app) authResponse(user store.User, csrf string) map[string]any {
	return map[string]any{
		"auth_enabled": a.authEnabled,
		"user":         user,
		"permissions":  auth.PermissionsForRole(user.Role),
		"csrf_token":   csrf,
	}
}

// authMe is public: the frontend uses 401 to decide whether to show the login
// screen, and reads the CSRF token once authenticated.
func (a *app) authMe(w http.ResponseWriter, r *http.Request) {
	if !a.authEnabled {
		writeJSON(w, http.StatusOK, map[string]any{
			"auth_enabled": false,
			"user":         map[string]any{"username": "local", "display_name": "本机用户", "role": auth.RoleAdmin},
			"permissions":  auth.PermissionsForRole(auth.RoleAdmin),
		})
		return
	}
	session, ok := a.sessionFromRequest(r)
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录")
		return
	}
	user := store.User{ID: session.UserID, Username: session.Username, DisplayName: session.DisplayName, Role: session.Role, Status: session.Status}
	writeJSON(w, http.StatusOK, a.authResponse(user, session.CSRF))
}

func (a *app) authLogin(w http.ResponseWriter, r *http.Request) {
	if !a.authEnabled {
		writeProblem(w, http.StatusConflict, "AUTH_DISABLED", "当前是本机单用户模式，未启用登录")
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" || in.Password == "" {
		writeProblem(w, http.StatusBadRequest, "INVALID_CREDENTIALS", "请输入用户名和密码")
		return
	}
	key := clientIP(r) + "|" + strings.ToLower(in.Username)
	if !a.limiter.allowed(key) {
		writeProblem(w, http.StatusTooManyRequests, "LOGIN_RATE_LIMITED", "登录尝试过多，请 5 分钟后再试")
		return
	}
	user, hash, err := a.store.GetUserByUsername(r.Context(), in.Username)
	if err != nil || user.Status != "active" || !auth.VerifyPassword(hash, in.Password) {
		a.limiter.fail(key)
		writeProblem(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "用户名或密码错误")
		return
	}
	a.limiter.reset(key)
	token, err := auth.NewToken()
	if err != nil {
		serverError(w, err)
		return
	}
	csrf, err := auth.NewToken()
	if err != nil {
		serverError(w, err)
		return
	}
	expires := time.Now().UTC().Add(a.sessionTTL)
	if err := a.store.CreateAuthSession(r.Context(), user.ID, auth.HashToken(token), csrf, expires, clientIP(r), r.UserAgent()); err != nil {
		serverError(w, err)
		return
	}
	a.store.TouchUserLogin(r.Context(), user.ID)
	setSessionCookie(w, r, token, expires)
	a.store.Audit(store.WithActor(r.Context(), user.ID), "auth.login", "user", user.ID, "success", map[string]any{"ip": clientIP(r)})
	writeJSON(w, http.StatusOK, a.authResponse(user, csrf))
}

func (a *app) authLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		a.store.DeleteAuthSession(r.Context(), auth.HashToken(cookie.Value))
	}
	clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.store.ListUsers(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": users})
}

func (a *app) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
		Role        string `json:"role"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" || len(in.Username) > 64 {
		writeProblem(w, http.StatusBadRequest, "INVALID_USERNAME", "用户名不能为空且不超过 64 个字符")
		return
	}
	if len(in.Password) < 8 {
		writeProblem(w, http.StatusBadRequest, "WEAK_PASSWORD", "密码至少 8 个字符")
		return
	}
	if !auth.ValidRole(in.Role) {
		writeProblem(w, http.StatusBadRequest, "INVALID_ROLE", "角色必须是 admin、operator、auditor 或 viewer")
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		serverError(w, err)
		return
	}
	user, err := a.store.CreateUser(r.Context(), in.Username, in.DisplayName, hash, in.Role)
	if err != nil {
		if store.IsUniqueViolation(err) {
			writeProblem(w, http.StatusConflict, "USERNAME_CONFLICT", "用户名已存在")
			return
		}
		writeProblem(w, http.StatusBadRequest, "INVALID_USER", err.Error())
		return
	}
	a.store.Audit(r.Context(), "auth.user.create", "user", user.ID, "success", map[string]any{"username": user.Username, "role": user.Role})
	writeJSON(w, http.StatusCreated, user)
}

func (a *app) updateUser(w http.ResponseWriter, r *http.Request) {
	target, err := a.store.GetUser(r.Context(), r.PathValue("id"))
	if err != nil {
		notFoundOrServer(w, err)
		return
	}
	var in struct {
		DisplayName *string `json:"display_name"`
		Role        *string `json:"role"`
		Status      *string `json:"status"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	newRole, newStatus := target.Role, target.Status
	if in.Role != nil {
		if !auth.ValidRole(*in.Role) {
			writeProblem(w, http.StatusBadRequest, "INVALID_ROLE", "角色无效")
			return
		}
		newRole = *in.Role
	}
	if in.Status != nil {
		if *in.Status != "active" && *in.Status != "disabled" {
			writeProblem(w, http.StatusBadRequest, "INVALID_STATUS", "状态必须是 active 或 disabled")
			return
		}
		newStatus = *in.Status
	}
	if err := a.guardLastAdmin(r.Context(), target, newRole, newStatus); err != nil {
		writeProblem(w, http.StatusConflict, "LAST_ADMIN", err.Error())
		return
	}
	if in.Role != nil {
		if err := a.store.UpdateUserRole(r.Context(), target.ID, newRole); err != nil {
			serverError(w, err)
			return
		}
	}
	if in.Status != nil {
		if err := a.store.UpdateUserStatus(r.Context(), target.ID, newStatus); err != nil {
			serverError(w, err)
			return
		}
	}
	if in.DisplayName != nil {
		if err := a.store.UpdateUserDisplayName(r.Context(), target.ID, strings.TrimSpace(*in.DisplayName)); err != nil {
			serverError(w, err)
			return
		}
	}
	a.store.Audit(r.Context(), "auth.user.update", "user", target.ID, "success", map[string]any{"role": newRole, "status": newStatus})
	updated, err := a.store.GetUser(r.Context(), target.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (a *app) disableUser(w http.ResponseWriter, r *http.Request) {
	target, err := a.store.GetUser(r.Context(), r.PathValue("id"))
	if err != nil {
		notFoundOrServer(w, err)
		return
	}
	if err := a.guardLastAdmin(r.Context(), target, target.Role, "disabled"); err != nil {
		writeProblem(w, http.StatusConflict, "LAST_ADMIN", err.Error())
		return
	}
	if err := a.store.UpdateUserStatus(r.Context(), target.ID, "disabled"); err != nil {
		serverError(w, err)
		return
	}
	a.store.Audit(r.Context(), "auth.user.disable", "user", target.ID, "success", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if len(in.Password) < 8 {
		writeProblem(w, http.StatusBadRequest, "WEAK_PASSWORD", "密码至少 8 个字符")
		return
	}
	target, err := a.store.GetUser(r.Context(), r.PathValue("id"))
	if err != nil {
		notFoundOrServer(w, err)
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		serverError(w, err)
		return
	}
	if err := a.store.UpdateUserPassword(r.Context(), target.ID, hash); err != nil {
		serverError(w, err)
		return
	}
	a.store.Audit(r.Context(), "auth.user.password", "user", target.ID, "success", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) guardLastAdmin(ctx context.Context, target store.User, newRole, newStatus string) error {
	if target.Role != auth.RoleAdmin {
		return nil
	}
	if newRole == auth.RoleAdmin && newStatus == "active" {
		return nil
	}
	users, err := a.store.ListUsers(ctx)
	if err != nil {
		return err
	}
	for _, user := range users {
		if user.ID != target.ID && user.Role == auth.RoleAdmin && user.Status == "active" {
			return nil
		}
	}
	return errLastAdmin
}

func (a *app) listPermissions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": auth.Permissions})
}

func (a *app) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 1000 {
			limit = parsed
		}
	}
	events, err := a.store.ListAuditEvents(r.Context(), limit)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": events, "count": len(events)})
}

func (a *app) getAssetGrants(w http.ResponseWriter, r *http.Request) {
	grants, err := a.store.ListAssetGrantsForAsset(r.Context(), r.PathValue("id"))
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": grants})
}

func (a *app) putAssetGrants(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Grants []store.AssetGrantInput `json:"grants"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	assetID := r.PathValue("id")
	if _, err := a.store.GetAsset(r.Context(), assetID); err != nil {
		notFoundOrServer(w, err)
		return
	}
	if err := a.store.ReplaceAssetGrants(r.Context(), assetID, in.Grants); err != nil {
		writeProblem(w, http.StatusBadRequest, "INVALID_GRANT", err.Error())
		return
	}
	a.store.Audit(r.Context(), "asset.grants.update", "asset", assetID, "success", map[string]any{"count": len(in.Grants)})
	grants, err := a.store.ListAssetGrantsForAsset(r.Context(), assetID)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": grants})
}

func (a *app) listUserGrants(w http.ResponseWriter, r *http.Request) {
	grants, err := a.store.ListAssetGrantsForUser(r.Context(), r.PathValue("id"))
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": grants})
}

var errLastAdmin = errLastAdminError{}

type errLastAdminError struct{}

func (errLastAdminError) Error() string { return "不能禁用或降级最后一个管理员" }

func (a *app) principal(r *http.Request) (auth.Principal, bool) {
	return auth.PrincipalFrom(r.Context())
}

func (a *app) isAdmin(r *http.Request) bool {
	principal, ok := a.principal(r)
	return ok && principal.Role == auth.RoleAdmin
}

func (a *app) assetVisible(r *http.Request, assetID string) bool {
	principal, ok := a.principal(r)
	if !ok {
		return true
	}
	if principal.Role == auth.RoleAdmin || auth.HasPermission(principal.Role, "asset.read.all") {
		return true
	}
	_, err := a.store.AssetGrantFor(r.Context(), principal.UserID, assetID)
	return err == nil
}

func (a *app) assetEditable(r *http.Request, assetID string) bool {
	principal, ok := a.principal(r)
	if !ok {
		return true
	}
	if principal.Role == auth.RoleAdmin {
		return true
	}
	if !auth.HasPermission(principal.Role, "asset.write") {
		return false
	}
	grant, err := a.store.AssetGrantFor(r.Context(), principal.UserID, assetID)
	return err == nil && grant.CanEdit
}

func (a *app) assetConnectable(r *http.Request, assetID, permission string) bool {
	principal, ok := a.principal(r)
	if !ok {
		return true
	}
	if principal.Role == auth.RoleAdmin {
		return true
	}
	if !auth.HasPermission(principal.Role, permission) {
		return false
	}
	grant, err := a.store.AssetGrantFor(r.Context(), principal.UserID, assetID)
	return err == nil && grant.CanConnect
}

// bootstrapAdmin creates the first administrator. A non-loopback deployment
// must supply QIYUN_ADMIN_PASSWORD; a local development run gets a one-time
// random password printed to the console.
func bootstrapAdmin(ctx context.Context, st *store.Store, loopback bool) error {
	username := strings.TrimSpace(os.Getenv("QIYUN_ADMIN_USER"))
	if username == "" {
		username = "admin"
	}
	password := os.Getenv("QIYUN_ADMIN_PASSWORD")
	if password == "" {
		if !loopback {
			return errors.New("首次启动必须设置 QIYUN_ADMIN_PASSWORD（至少 8 位）")
		}
		token, err := auth.NewToken()
		if err != nil {
			return err
		}
		password = token[:16]
		slog.Warn("generated bootstrap administrator password; change it after first login", "username", username, "password", password)
	}
	if len(password) < 8 {
		return errors.New("QIYUN_ADMIN_PASSWORD 至少需要 8 个字符")
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := st.CreateUser(ctx, username, "系统管理员", hash, auth.RoleAdmin); err != nil {
		return err
	}
	slog.Info("bootstrap administrator created", "username", username)
	return nil
}

func (a *app) visibleAssetIDs(r *http.Request) map[string]bool {
	principal, ok := a.principal(r)
	if !ok {
		return nil
	}
	if principal.Role == auth.RoleAdmin || auth.HasPermission(principal.Role, "asset.read.all") {
		return nil
	}
	grants, err := a.store.ListAssetGrantsForUser(r.Context(), principal.UserID)
	if err != nil {
		return map[string]bool{}
	}
	visible := make(map[string]bool, len(grants))
	for _, grant := range grants {
		visible[grant.AssetID] = true
	}
	return visible
}
