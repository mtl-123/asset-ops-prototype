package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"qiyun-asset-ops/internal/secretbox"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	box, err := secretbox.LoadOrCreate(filepath.Join(dir, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := Open(filepath.Join(dir, "test.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestAssetCredentialLifecycle(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	asset, err := st.CreateAsset(ctx, CreateAsset{AssetCode: "SRV-001", Name: "production", Address: "10.2.3.4", SSHPort: 22, Department: "研发", Environment: "production", Username: "ops", AuthType: "password", Secret: "never-return-this"})
	if err != nil {
		t.Fatal(err)
	}
	if asset.HostKeyStatus != "pending" {
		t.Fatalf("status = %s", asset.HostKeyStatus)
	}
	raw, _ := json.Marshal(asset)
	if strings.Contains(string(raw), "never-return-this") {
		t.Fatal("asset response leaked secret")
	}
	got, cred, err := st.CredentialForAsset(ctx, asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != asset.ID || cred.Secret != "never-return-this" || cred.Username != "ops" {
		t.Fatalf("bad credential result: %#v %#v", got, cred)
	}
	if err := st.ApproveHostKey(ctx, asset.ID, "ssh-ed25519 AAAATEST", "SHA256:test"); err != nil {
		t.Fatal(err)
	}
	got, err = st.GetAsset(ctx, asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.HostKeyStatus != "verified" || got.HostKeyFingerprint != "SHA256:test" {
		t.Fatalf("host key not approved: %#v", got)
	}
	if err := st.DeleteAsset(ctx, asset.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetAsset(ctx, asset.ID); err == nil {
		t.Fatal("deleted asset remains visible")
	}
}

func TestAssetCodeUnique(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	in := CreateAsset{AssetCode: "DUP", Name: "one", Address: "10.0.0.1", Username: "ops", AuthType: "password", Secret: "x"}
	if _, err := st.CreateAsset(ctx, in); err != nil {
		t.Fatal(err)
	}
	in.Name = "two"
	if _, err := st.CreateAsset(ctx, in); !IsUniqueViolation(err) {
		t.Fatalf("expected unique violation, got %v", err)
	}
}

func TestAssetCodeReusableAfterDelete(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	in := CreateAsset{AssetCode: "REUSE-1", Name: "first", Address: "10.4.4.4", Username: "ops", AuthType: "password", Secret: "pw"}
	first, err := st.CreateAsset(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteAsset(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	in.Name = "second"
	second, err := st.CreateAsset(ctx, in)
	if err != nil {
		t.Fatalf("re-using the code of a deleted asset must succeed, got %v", err)
	}
	if second.AssetCode != "REUSE-1" || second.ID == first.ID {
		t.Fatalf("unexpected recreated asset: %#v", second)
	}
	in.Name = "third"
	if _, err := st.CreateAsset(ctx, in); !IsUniqueViolation(err) {
		t.Fatalf("active duplicate must still be rejected, got %v", err)
	}
}

func TestLegacyUniqueConstraintIsRebuilt(t *testing.T) {
	dir := t.TempDir()
	box, err := secretbox.LoadOrCreate(filepath.Join(dir, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "legacy.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// First-release schema: table-level UNIQUE and no ledger columns.
	if _, err := legacy.Exec(`CREATE TABLE credentials (id TEXT PRIMARY KEY, username TEXT NOT NULL, auth_type TEXT NOT NULL, nonce BLOB NOT NULL, ciphertext BLOB NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE assets (id TEXT PRIMARY KEY, asset_code TEXT NOT NULL UNIQUE, name TEXT NOT NULL, address TEXT NOT NULL, ssh_port INTEGER NOT NULL, os_family TEXT NOT NULL DEFAULT 'linux', os_name TEXT NOT NULL DEFAULT '', owner TEXT NOT NULL DEFAULT '', department TEXT NOT NULL DEFAULT '', environment TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'unknown', credential_id TEXT NOT NULL REFERENCES credentials(id), host_key TEXT NOT NULL DEFAULT '', host_key_fingerprint TEXT NOT NULL DEFAULT '', version INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, deleted_at TEXT);`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path, box)
	if err != nil {
		t.Fatalf("migration must upgrade the legacy schema, got %v", err)
	}
	defer st.Close()
	ctx := context.Background()
	asset, err := st.CreateAsset(ctx, CreateAsset{AssetCode: "LEGACY-1", Name: "a", Address: "10.5.5.5", Username: "ops", AuthType: "password", Secret: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteAsset(ctx, asset.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateAsset(ctx, CreateAsset{AssetCode: "LEGACY-1", Name: "b", Address: "10.5.5.6", Username: "ops", AuthType: "password", Secret: "pw"}); err != nil {
		t.Fatalf("legacy database must accept a reused code after delete, got %v", err)
	}
}

func TestUpdateAssetInvalidatesHostKeyOnAddressChange(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	asset, err := st.CreateAsset(ctx, CreateAsset{AssetCode: "EDIT-1", Name: "old", Address: "10.7.7.7", SSHPort: 22, Username: "ops", AuthType: "password", Secret: "pw", Department: "A"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ApproveHostKey(ctx, asset.ID, "ssh-ed25519 AAAATEST", "SHA256:abc"); err != nil {
		t.Fatal(err)
	}
	name, owner := "new name", "new owner"
	updated, err := st.UpdateAsset(ctx, asset.ID, AssetPatch{Name: &name, Owner: &owner})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != name || updated.Owner != owner {
		t.Fatalf("patch not applied: %#v", updated)
	}
	if updated.HostKeyStatus != "verified" {
		t.Fatal("renaming must not invalidate the approved host key")
	}
	address := "10.7.7.8"
	updated, err = st.UpdateAsset(ctx, asset.ID, AssetPatch{Address: &address})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Address != address {
		t.Fatalf("address not updated: %#v", updated)
	}
	if updated.HostKeyStatus != "pending" || updated.HostKeyFingerprint != "" {
		t.Fatalf("changing the address must require a new fingerprint approval: %#v", updated)
	}
	port := 2222
	updated, err = st.UpdateAsset(ctx, asset.ID, AssetPatch{SSHPort: &port})
	if err != nil {
		t.Fatal(err)
	}
	if updated.SSHPort != 2222 {
		t.Fatalf("port not updated: %#v", updated)
	}
	if _, err := st.UpdateAsset(ctx, asset.ID, AssetPatch{}); err == nil {
		t.Fatal("empty patch must be rejected")
	}
	badPort := 0
	if _, err := st.UpdateAsset(ctx, asset.ID, AssetPatch{SSHPort: &badPort}); err == nil {
		t.Fatal("invalid port must be rejected")
	}
}

func TestSearchAssetsFilters(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	mk := func(code, dept, env string) Asset {
		a, err := st.CreateAsset(ctx, CreateAsset{AssetCode: code, Name: code, Address: "10.8.0.1", Username: "ops", AuthType: "password", Secret: "pw", Department: dept, Environment: env})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := mk("F-1", "研发部", "production")
	mk("F-2", "运维部", "testing")
	if _, err := st.SearchAssets(ctx, AssetFilter{Department: "研发部"}); err != nil {
		t.Fatal(err)
	}
	items, err := st.SearchAssets(ctx, AssetFilter{Department: "研发部"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].AssetCode != "F-1" {
		t.Fatalf("department filter failed: %#v", items)
	}
	pending, err := st.SearchAssets(ctx, AssetFilter{HostKey: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending host keys, got %d", len(pending))
	}
	if err := st.ApproveHostKey(ctx, a.ID, "ssh-ed25519 AAAATEST", "SHA256:x"); err != nil {
		t.Fatal(err)
	}
	verified, err := st.SearchAssets(ctx, AssetFilter{HostKey: "verified"})
	if err != nil {
		t.Fatal(err)
	}
	if len(verified) != 1 || verified[0].AssetCode != "F-1" {
		t.Fatalf("verified filter failed: %#v", verified)
	}
}

func TestUpdateCredentialKeepsApprovedHostKey(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	asset, err := st.CreateAsset(ctx, CreateAsset{AssetCode: "ROT-1", Name: "rotate", Address: "10.9.9.9", Username: "ops", AuthType: "password", Secret: "old-password"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ApproveHostKey(ctx, asset.ID, "ssh-ed25519 AAAATEST", "SHA256:keepme"); err != nil {
		t.Fatal(err)
	}
	updated, err := st.UpdateCredential(ctx, asset.ID, CreateAsset{Username: "ops", AuthType: "password", Secret: "new-password"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.HostKeyFingerprint != "SHA256:keepme" || updated.HostKeyStatus != "verified" {
		t.Fatalf("host key must survive credential rotation: %#v", updated)
	}
	if updated.Version <= asset.Version {
		t.Fatalf("version must increase, got %d -> %d", asset.Version, updated.Version)
	}
	_, cred, err := st.CredentialForAsset(ctx, asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cred.Secret != "new-password" {
		t.Fatalf("credential not replaced, got %q", cred.Secret)
	}
}

func TestCreateRejectsUnusableCredentialEarly(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	base := CreateAsset{AssetCode: "BAD-1", Name: "bad", Address: "10.0.0.2", Username: "ops"}
	passwordAsKey := base
	passwordAsKey.AuthType = "private_key"
	passwordAsKey.Secret = "not-a-key"
	if _, err := st.CreateAsset(ctx, passwordAsKey); err == nil || !strings.Contains(err.Error(), "私钥") {
		t.Fatalf("expected private key explanation, got %v", err)
	}
	keyAsPassword := base
	keyAsPassword.AuthType = "password"
	keyAsPassword.Secret = "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----"
	if _, err := st.CreateAsset(ctx, keyAsPassword); err == nil || !strings.Contains(err.Error(), "私钥") {
		t.Fatalf("expected wrong-type explanation, got %v", err)
	}
	empty := base
	empty.AuthType = "password"
	empty.Secret = "  "
	if _, err := st.CreateAsset(ctx, empty); err == nil {
		t.Fatal("blank password must be rejected before persisting")
	}
}
