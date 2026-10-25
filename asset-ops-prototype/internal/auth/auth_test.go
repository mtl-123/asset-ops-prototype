package auth

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"slices"
	"strings"
	"testing"
)

func TestPermissionsCatalog(t *testing.T) {
	want := []Permission{
		{Code: "asset.read", Description: "查看资产"},
		{Code: "asset.read.all", Description: "查看全部资产"},
		{Code: "asset.write", Description: "编辑资产"},
		{Code: "credential.write", Description: "修改凭据"},
		{Code: "ssh.connect", Description: "SSH 连接"},
		{Code: "rdp.connect", Description: "RDP 连接"},
		{Code: "user.manage", Description: "用户管理"},
		{Code: "audit.read", Description: "审计查看"},
	}
	if !slices.Equal(Permissions, want) {
		t.Fatalf("Permissions = %#v, want %#v", Permissions, want)
	}
}

func TestValidRole(t *testing.T) {
	for _, role := range []string{RoleAdmin, RoleOperator, RoleAuditor, RoleViewer} {
		if !ValidRole(role) {
			t.Errorf("ValidRole(%q) = false, want true", role)
		}
	}
	for _, role := range []string{"", "Admin", "root", "admin "} {
		if ValidRole(role) {
			t.Errorf("ValidRole(%q) = true, want false", role)
		}
	}
}

func TestPermissionsForRole(t *testing.T) {
	adminCodes := make([]string, len(Permissions))
	for i, permission := range Permissions {
		adminCodes[i] = permission.Code
	}

	cases := []struct {
		role string
		want []string
	}{
		{
			role: RoleAdmin,
			want: adminCodes,
		},
		{
			role: RoleOperator,
			want: []string{
				"asset.read",
				"asset.write",
				"credential.write",
				"ssh.connect",
				"rdp.connect",
			},
		},
		{
			role: RoleAuditor,
			want: []string{"asset.read", "asset.read.all", "audit.read"},
		},
		{
			role: RoleViewer,
			want: []string{"asset.read"},
		},
		{
			role: "unknown",
			want: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			got := PermissionsForRole(tc.role)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("PermissionsForRole(%q) = %v, want %v", tc.role, got, tc.want)
			}
		})
	}
}

func TestHasPermission(t *testing.T) {
	cases := []struct {
		name string
		role string
		code string
		want bool
	}{
		{name: "admin has a known permission", role: RoleAdmin, code: "asset.read", want: true},
		{name: "admin has an unknown permission", role: RoleAdmin, code: "does.not.exist", want: true},
		{name: "admin has wildcard", role: RoleAdmin, code: "*", want: true},
		{name: "operator reads assets", role: RoleOperator, code: "asset.read", want: true},
		{name: "operator writes assets", role: RoleOperator, code: "asset.write", want: true},
		{name: "operator modifies credentials", role: RoleOperator, code: "credential.write", want: true},
		{name: "operator has ssh", role: RoleOperator, code: "ssh.connect", want: true},
		{name: "operator has rdp", role: RoleOperator, code: "rdp.connect", want: true},
		{name: "operator cannot read audit", role: RoleOperator, code: "audit.read", want: false},
		{name: "auditor reads assets", role: RoleAuditor, code: "asset.read", want: true},
		{name: "auditor reads all assets", role: RoleAuditor, code: "asset.read.all", want: true},
		{name: "auditor reads audit", role: RoleAuditor, code: "audit.read", want: true},
		{name: "auditor cannot write assets", role: RoleAuditor, code: "asset.write", want: false},
		{name: "viewer reads assets", role: RoleViewer, code: "asset.read", want: true},
		{name: "viewer cannot write assets", role: RoleViewer, code: "asset.write", want: false},
		{name: "unknown role has no permissions", role: "unknown", code: "asset.read", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasPermission(tc.role, tc.code); got != tc.want {
				t.Fatalf("HasPermission(%q, %q) = %v, want %v", tc.role, tc.code, got, tc.want)
			}
		})
	}
}

func TestCanReadAllAssets(t *testing.T) {
	cases := []struct {
		role string
		want bool
	}{
		{role: RoleAdmin, want: true},
		{role: RoleAuditor, want: true},
		{role: RoleOperator, want: false},
		{role: RoleViewer, want: false},
		{role: "unknown", want: false},
	}
	for _, tc := range cases {
		if got := CanReadAllAssets(tc.role); got != tc.want {
			t.Errorf("CanReadAllAssets(%q) = %v, want %v", tc.role, got, tc.want)
		}
	}
}

const testPassword = "correct horse battery staple"

func TestHashPasswordAndVerify(t *testing.T) {
	encoded, err := HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	parts := strings.Split(encoded, "$")
	if len(parts) != 4 {
		t.Fatalf("hash has %d fields, want 4: %q", len(parts), encoded)
	}
	if parts[0] != "pbkdf2-sha256" {
		t.Errorf("algorithm = %q, want pbkdf2-sha256", parts[0])
	}
	if parts[1] != "600000" {
		t.Errorf("iterations = %q, want 600000", parts[1])
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode salt: %v", err)
	}
	if len(salt) != 16 {
		t.Errorf("salt length = %d, want 16", len(salt))
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	if len(key) != 32 {
		t.Errorf("key length = %d, want 32", len(key))
	}

	if !VerifyPassword(encoded, testPassword) {
		t.Fatal("VerifyPassword rejected the correct password")
	}
	if VerifyPassword(encoded, testPassword+"!") {
		t.Fatal("VerifyPassword accepted an incorrect password")
	}
	if VerifyPassword(encoded, "") {
		t.Fatal("VerifyPassword accepted an empty password")
	}
}

func TestHashPasswordUsesFreshSalt(t *testing.T) {
	first, err := HashPassword(testPassword)
	if err != nil {
		t.Fatalf("first HashPassword: %v", err)
	}
	second, err := HashPassword(testPassword)
	if err != nil {
		t.Fatalf("second HashPassword: %v", err)
	}
	if first == second {
		t.Fatal("two hashes of the same password are identical")
	}

	firstParts := strings.Split(first, "$")
	secondParts := strings.Split(second, "$")
	if len(firstParts) != 4 || len(secondParts) != 4 {
		t.Fatalf("unexpected hash encoding: %q, %q", first, second)
	}
	if firstParts[2] == secondParts[2] {
		t.Fatal("two hashes of the same password reused the salt")
	}
	if !VerifyPassword(first, testPassword) || !VerifyPassword(second, testPassword) {
		t.Fatal("a generated hash did not verify")
	}
}

func TestHashPasswordRejectsEmptyPassword(t *testing.T) {
	encoded, err := HashPassword("")
	if err == nil {
		t.Fatalf("HashPassword(\"\") = %q, want an error", encoded)
	}
	if encoded != "" {
		t.Errorf("HashPassword(\"\") = %q, want an empty hash on error", encoded)
	}
}

func TestVerifyPasswordRejectsTampering(t *testing.T) {
	encoded, err := HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 {
		t.Fatalf("unexpected hash encoding: %q", encoded)
	}

	tamperedSalt := tamperBase64(t, parts[2])
	tamperedKey := tamperBase64(t, parts[3])

	cases := []struct {
		name    string
		encoded string
	}{
		{name: "empty", encoded: ""},
		{name: "missing field", encoded: strings.Join(parts[:3], "$")},
		{name: "extra field", encoded: encoded + "$extra"},
		{name: "unknown algorithm", encoded: replaceHashPart(parts, 0, "pbkdf2-sha1")},
		{name: "non-numeric iterations", encoded: replaceHashPart(parts, 1, "not-a-number")},
		{name: "unsupported iterations", encoded: replaceHashPart(parts, 1, "1")},
		{name: "invalid salt encoding", encoded: replaceHashPart(parts, 2, "!!!")},
		{name: "short salt", encoded: replaceHashPart(parts, 2, base64.RawStdEncoding.EncodeToString(make([]byte, 15)))},
		{name: "invalid key encoding", encoded: replaceHashPart(parts, 3, "!!!")},
		{name: "short key", encoded: replaceHashPart(parts, 3, base64.RawStdEncoding.EncodeToString(make([]byte, 31)))},
		{name: "tampered salt", encoded: replaceHashPart(parts, 2, tamperedSalt)},
		{name: "tampered key", encoded: replaceHashPart(parts, 3, tamperedKey)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if VerifyPassword(tc.encoded, testPassword) {
				t.Fatalf("VerifyPassword accepted tampered encoding %q", tc.encoded)
			}
		})
	}
}

func replaceHashPart(parts []string, index int, value string) string {
	clone := append([]string(nil), parts...)
	clone[index] = value
	return strings.Join(clone, "$")
}

func tamperBase64(t *testing.T, encoded string) string {
	t.Helper()
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode %q: %v", encoded, err)
	}
	if len(raw) == 0 {
		t.Fatal("cannot tamper with empty base64 value")
	}
	raw[0] ^= 0x01
	return base64.RawStdEncoding.EncodeToString(raw)
}

func TestNewToken(t *testing.T) {
	seen := make(map[string]struct{}, 32)
	for i := 0; i < 32; i++ {
		token, err := NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		if token == "" {
			t.Fatal("NewToken returned an empty token")
		}
		raw, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			t.Fatalf("token %q is not raw URL-safe base64: %v", token, err)
		}
		if len(raw) != 32 {
			t.Fatalf("token decodes to %d bytes, want 32", len(raw))
		}
		if strings.ContainsAny(token, "+/=") {
			t.Fatalf("token %q is not raw URL-safe base64", token)
		}
		if _, exists := seen[token]; exists {
			t.Fatalf("NewToken returned a duplicate token %q", token)
		}
		seen[token] = struct{}{}
	}
}

func TestHashToken(t *testing.T) {
	const token = "opaque-session-token"
	first := HashToken(token)
	second := HashToken(token)
	if first != second {
		t.Fatalf("HashToken is not stable: %q != %q", first, second)
	}
	if len(first) != 64 {
		t.Fatalf("HashToken length = %d, want 64", len(first))
	}
	if strings.ToLower(first) != first {
		t.Fatalf("HashToken = %q, want lowercase hex", first)
	}
	if _, err := hex.DecodeString(first); err != nil {
		t.Fatalf("HashToken = %q, want hex: %v", first, err)
	}
	const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := HashToken(""); got != emptySHA256 {
		t.Errorf("HashToken(\"\") = %q, want %q", got, emptySHA256)
	}
}

func TestPrincipalAndCSRFContext(t *testing.T) {
	ctx := context.Background()
	if principal, ok := PrincipalFrom(ctx); ok {
		t.Fatalf("PrincipalFrom(background) = %#v, true; want false", principal)
	}
	if token := CSRFFrom(ctx); token != "" {
		t.Fatalf("CSRFFrom(background) = %q, want empty", token)
	}

	want := Principal{
		UserID:      "user-42",
		Username:    "alice",
		DisplayName: "Alice",
		Role:        RoleOperator,
	}
	ctx = WithPrincipal(ctx, want)
	ctx = WithCSRF(ctx, "csrf-token-value")

	got, ok := PrincipalFrom(ctx)
	if !ok {
		t.Fatal("PrincipalFrom did not find the stored principal")
	}
	if got != want {
		t.Fatalf("PrincipalFrom = %#v, want %#v", got, want)
	}
	if token := CSRFFrom(ctx); token != "csrf-token-value" {
		t.Fatalf("CSRFFrom = %q, want csrf-token-value", token)
	}
}
