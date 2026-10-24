package sshgateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"qiyun-asset-ops/internal/secretbox"
	"qiyun-asset-ops/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	box, err := secretbox.LoadOrCreate(filepath.Join(dir, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "test.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func testPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestHostKeyCallbackRejectsMismatch(t *testing.T) {
	expected := testPublicKey(t)
	callback, err := hostKeyCallback(strings.TrimSpace(string(ssh.MarshalAuthorizedKey(expected))))
	if err != nil {
		t.Fatal(err)
	}
	if err := callback("host", &net.TCPAddr{}, expected); err != nil {
		t.Fatalf("expected key rejected: %v", err)
	}
	if err := callback("host", &net.TCPAddr{}, testPublicKey(t)); err == nil || !strings.Contains(err.Error(), "HOST_KEY_MISMATCH") {
		t.Fatalf("mismatch not rejected: %v", err)
	}
}

func TestResolveTargetRejectsLoopbackByDefault(t *testing.T) {
	m, err := New(testStore(t), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.ResolveTarget(context.Background(), store.Asset{Address: "127.0.0.1", SSHPort: 22})
	if err == nil || !strings.Contains(err.Error(), "outside QIYUN_ALLOWED_CIDRS") {
		t.Fatalf("loopback should be rejected, got %v", err)
	}
}

func TestCreateTicketRequiresApprovedHostKey(t *testing.T) {
	st := testStore(t)
	asset, err := st.CreateAsset(context.Background(), store.CreateAsset{AssetCode: "SSH-1", Name: "ssh", Address: "10.0.0.2", SSHPort: 22, Username: "ops", AuthType: "password", Secret: "x"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(st, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.CreateTicket(asset.ID); err == nil || !strings.Contains(err.Error(), "HOST_KEY_UNKNOWN") {
		t.Fatalf("ticket issued before approval: %v", err)
	}
}
