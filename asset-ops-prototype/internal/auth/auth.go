// Package auth provides the authentication and authorization primitives used
// by central multi-user mode.
package auth

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Roles supported by central user management.
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleAuditor  = "auditor"
	RoleViewer   = "viewer"
)

// Permission describes one capability that can be granted by a role.
type Permission struct {
	Code        string `json:"code"`
	Description string `json:"description"`
}

const (
	permissionAssetRead       = "asset.read"
	permissionAssetReadAll    = "asset.read.all"
	permissionAssetWrite      = "asset.write"
	permissionCredentialWrite = "credential.write"
	permissionSSHConnect      = "ssh.connect"
	permissionRDPConnect      = "rdp.connect"
	permissionUserManage      = "user.manage"
	permissionAuditRead       = "audit.read"
)

// Permissions is the complete catalog of permissions understood by the
// application. The order is used when describing the administrator's grants.
var Permissions = []Permission{
	{Code: permissionAssetRead, Description: "查看资产"},
	{Code: permissionAssetReadAll, Description: "查看全部资产"},
	{Code: permissionAssetWrite, Description: "编辑资产"},
	{Code: permissionCredentialWrite, Description: "修改凭据"},
	{Code: permissionSSHConnect, Description: "SSH 连接"},
	{Code: permissionRDPConnect, Description: "RDP 连接"},
	{Code: permissionUserManage, Description: "用户管理"},
	{Code: permissionAuditRead, Description: "审计查看"},
}

var rolePermissions = map[string][]string{
	RoleOperator: {
		permissionAssetRead,
		permissionAssetWrite,
		permissionCredentialWrite,
		permissionSSHConnect,
		permissionRDPConnect,
	},
	RoleAuditor: {
		permissionAssetRead,
		permissionAssetReadAll,
		permissionAuditRead,
	},
	RoleViewer: {
		permissionAssetRead,
	},
}

// ValidRole reports whether role is one of the roles supported by the
// application.
func ValidRole(role string) bool {
	switch role {
	case RoleAdmin, RoleOperator, RoleAuditor, RoleViewer:
		return true
	default:
		return false
	}
}

// PermissionsForRole returns the permission codes granted to role. The
// administrator receives every permission in the catalog; an unknown role
// receives an empty slice.
func PermissionsForRole(role string) []string {
	if role == RoleAdmin {
		codes := make([]string, len(Permissions))
		for i, permission := range Permissions {
			codes[i] = permission.Code
		}
		return codes
	}

	codes, ok := rolePermissions[role]
	if !ok {
		return []string{}
	}
	return append([]string{}, codes...)
}

// HasPermission reports whether role grants code. The administrator has every
// permission.
func HasPermission(role, code string) bool {
	if role == RoleAdmin {
		return true
	}
	for _, granted := range rolePermissions[role] {
		if granted == code {
			return true
		}
	}
	return false
}

// CanReadAllAssets reports whether role may list and read every asset rather
// than only the assets assigned to that user.
func CanReadAllAssets(role string) bool {
	return HasPermission(role, permissionAssetReadAll) || HasPermission(role, "*")
}

const (
	passwordScheme     = "pbkdf2-sha256"
	passwordIterations = 600000
	passwordSaltSize   = 16
	passwordKeySize    = 32
	tokenSize          = 32
)

// HashPassword derives a PBKDF2-SHA256 password hash using a fresh random salt.
// The result is encoded as
//
//	pbkdf2-sha256$600000$<base64rawstd(salt)>$<base64rawstd(key)>
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("password must not be empty")
	}

	salt := make([]byte, passwordSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}

	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, passwordKeySize)
	if err != nil {
		return "", fmt.Errorf("derive password hash: %w", err)
	}

	encodedSalt := base64.RawStdEncoding.EncodeToString(salt)
	encodedKey := base64.RawStdEncoding.EncodeToString(key)
	return fmt.Sprintf("%s$%d$%s$%s", passwordScheme, passwordIterations, encodedSalt, encodedKey), nil
}

// VerifyPassword reports whether password matches a hash produced by
// HashPassword. Malformed encodings and unsupported iteration counts are
// rejected without revealing which part of the input was invalid.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != passwordScheme {
		return false
	}

	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations != passwordIterations {
		return false
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) != passwordSaltSize {
		return false
	}

	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(want) != passwordKeySize {
		return false
	}

	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// NewToken returns a new opaque token containing 32 bytes of cryptographic
// randomness, encoded with unpadded URL-safe base64.
func NewToken() (string, error) {
	token := make([]byte, tokenSize)
	if _, err := rand.Read(token); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(token), nil
}

// HashToken returns the lowercase SHA-256 hex digest used to persist a token
// without storing the bearer value itself.
func HashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

// Principal is the authenticated central user attached to a request context.
type Principal struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

type contextKey uint8

const (
	principalContextKey contextKey = iota
	csrfContextKey
)

// WithPrincipal returns a context carrying the authenticated user.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalContextKey, p)
}

// PrincipalFrom returns the authenticated user stored in ctx, if any.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalContextKey).(Principal)
	return p, ok
}

// WithCSRF returns a context carrying the CSRF token for the current session.
func WithCSRF(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, csrfContextKey, token)
}

// CSRFFrom returns the CSRF token stored in ctx, or an empty string when the
// context does not carry one.
func CSRFFrom(ctx context.Context) string {
	token, _ := ctx.Value(csrfContextKey).(string)
	return token
}
