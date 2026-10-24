package sshgateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"qiyun-asset-ops/internal/secretbox"
	"qiyun-asset-ops/internal/store"
)

// testSSHServer is a real in-process SSH server: real transport, real key
// exchange, real password and public-key authentication. It lets the connection
// diagnosis be verified end to end without depending on an external host.
type testSSHServer struct {
	addr       string
	hostSigner ssh.Signer
	authorized ssh.PublicKey
	userKey    ed25519.PrivateKey
	password   string
	user       string
}

func newTestSSHServer(t *testing.T) *testSSHServer {
	t.Helper()
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}
	_, userPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	userSigner, err := ssh.NewSignerFromKey(userPriv)
	if err != nil {
		t.Fatal(err)
	}
	server := &testSSHServer{hostSigner: hostSigner, authorized: userSigner.PublicKey(), userKey: userPriv, password: "correct-horse", user: "ops"}

	cfg := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if meta.User() == server.user && string(pass) == server.password {
				return nil, nil
			}
			return nil, fmt.Errorf("password rejected for %s", meta.User())
		},
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if meta.User() == server.user && bytes.Equal(key.Marshal(), server.authorized.Marshal()) {
				return nil, nil
			}
			return nil, fmt.Errorf("public key rejected for %s", meta.User())
		},
	}
	cfg.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.addr = listener.Addr().String()
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serveConn(conn, cfg)
		}
	}()
	return server
}

func (s *testSSHServer) serveConn(conn net.Conn, cfg *ssh.ServerConfig) {
	defer conn.Close()
	serverConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer serverConn.Close()
	go ssh.DiscardRequests(reqs)
	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "only session")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer channel.Close()
			for req := range requests {
				switch req.Type {
				case "exec":
					_, _ = channel.Write([]byte("ok\n"))
					_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
					req.Reply(true, nil)
					return
				case "shell", "pty-req", "window-change", "env":
					_ = req.Reply(true, nil)
				case "signal":
					_ = req.Reply(true, nil)
				default:
					_ = req.Reply(false, nil)
				}
			}
		}()
	}
}

func (s *testSSHServer) port(t *testing.T) int {
	t.Helper()
	_, portText, err := net.SplitHostPort(s.addr)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// openSSHKey renders the test user's private key in the same OpenSSH PEM form a
// real id_ed25519 file has.
func (s *testSSHServer) openSSHKey(t *testing.T) string {
	t.Helper()
	block, err := ssh.MarshalPrivateKey(s.userKey, "qiyun-test")
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(block))
}

func newTestGateway(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	box, err := secretbox.LoadOrCreate(dir + "/master.key")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dir+"/test.db", box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	// The in-process server only listens on loopback, so the allow list must
	// include it for this test alone.
	manager, err := New(st, []string{"127.0.0.0/8"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return manager, st
}

// approvedAsset creates the asset, scans and approves the host key exactly as the
// UI flow does.
func approvedAsset(t *testing.T, manager *Manager, st *store.Store, server *testSSHServer, in store.CreateAsset) store.Asset {
	t.Helper()
	in.Address = "127.0.0.1"
	in.SSHPort = server.port(t)
	asset, err := st.CreateAsset(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	key, fingerprint, _, err := manager.ScanHostKey(context.Background(), asset)
	if err != nil {
		t.Fatalf("scan host key: %v", err)
	}
	if err := st.ApproveHostKey(context.Background(), asset.ID, key, fingerprint); err != nil {
		t.Fatal(err)
	}
	return asset
}

func TestDiagnosePasswordAuthSucceeds(t *testing.T) {
	manager, st := newTestGateway(t)
	server := newTestSSHServer(t)
	asset := approvedAsset(t, manager, st, server, store.CreateAsset{AssetCode: "PWD-1", Name: "pwd", Username: "ops", AuthType: "password", Secret: "correct-horse"})

	d, err := manager.Diagnose(context.Background(), asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !d.OK {
		t.Fatalf("diagnose should pass, got %q (%v)", d.Summary, d.Stages)
	}
	if auth := stageByName(d, "authenticate"); !strings.Contains(auth.Detail, "password") {
		t.Fatalf("expected negotiated method in detail, got %q", auth.Detail)
	}
}

func TestDiagnoseWrongPasswordExplainsFailure(t *testing.T) {
	manager, st := newTestGateway(t)
	server := newTestSSHServer(t)
	asset := approvedAsset(t, manager, st, server, store.CreateAsset{AssetCode: "PWD-2", Name: "pwd", Username: "ops", AuthType: "password", Secret: "wrong-password"})

	d, err := manager.Diagnose(context.Background(), asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.OK {
		t.Fatal("wrong password must not pass")
	}
	failed := lastStage(d)
	if failed.OK || failed.Stage != "authenticate" {
		t.Fatalf("expected authenticate failure, got %#v", failed)
	}
	if failed.Hint == "" || !strings.Contains(failed.Hint, "私钥") {
		t.Fatalf("expected actionable hint, got %q", failed.Hint)
	}
}

func TestDiagnosePrivateKeySucceeds(t *testing.T) {
	manager, st := newTestGateway(t)
	server := newTestSSHServer(t)
	asset := approvedAsset(t, manager, st, server, store.CreateAsset{AssetCode: "KEY-1", Name: "key", Username: "ops", AuthType: "private_key", Secret: server.openSSHKey(t)})

	d, err := manager.Diagnose(context.Background(), asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !d.OK {
		t.Fatalf("authorized key should pass, got %q (%v)", d.Summary, d.Stages)
	}
	if !strings.HasPrefix(d.KeyFingerprint, "SHA256:") {
		t.Fatalf("expected key fingerprint for comparison, got %q", d.KeyFingerprint)
	}
	if !strings.HasPrefix(d.AuthorizedKey, "ssh-ed25519 ") {
		t.Fatalf("expected authorized_keys line, got %q", d.AuthorizedKey)
	}
}

func TestDiagnoseUnauthorizedKeyExplainsFailure(t *testing.T) {
	manager, st := newTestGateway(t)
	server := newTestSSHServer(t)
	// A valid but different key: this is the classic "key not in authorized_keys".
	_, otherPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(otherPriv, "other")
	if err != nil {
		t.Fatal(err)
	}
	asset := approvedAsset(t, manager, st, server, store.CreateAsset{AssetCode: "KEY-2", Name: "key", Username: "ops", AuthType: "private_key", Secret: string(pem.EncodeToMemory(block))})

	d, err := manager.Diagnose(context.Background(), asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.OK {
		t.Fatal("unauthorized key must not pass")
	}
	if lastStage(d).Stage != "authenticate" {
		t.Fatalf("expected authenticate failure, got %#v", lastStage(d))
	}
	if d.KeyFingerprint == "" {
		t.Fatal("fingerprint must be reported so operators can compare authorized_keys")
	}
}

func TestDiagnoseHostKeyMismatchIsReported(t *testing.T) {
	manager, st := newTestGateway(t)
	server := newTestSSHServer(t)
	asset := approvedAsset(t, manager, st, server, store.CreateAsset{AssetCode: "HK-1", Name: "hk", Username: "ops", AuthType: "password", Secret: "correct-horse"})

	// A different server on the same address simulates a replaced host key.
	replacement := newTestSSHServer(t)
	if err := st.ApproveHostKey(context.Background(), asset.ID, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(replacement.hostSigner.PublicKey()))), ssh.FingerprintSHA256(replacement.hostSigner.PublicKey())); err != nil {
		t.Fatal(err)
	}
	d, err := manager.Diagnose(context.Background(), asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.OK {
		t.Fatal("host key mismatch must block the connection")
	}
	if lastStage(d).Stage != "host_key_verify" || !strings.Contains(lastStage(d).Detail, "HOST_KEY_MISMATCH") {
		t.Fatalf("expected host key mismatch, got %#v", lastStage(d))
	}
}

func TestDiagnoseUnapprovedHostKeyBlocksBeforeDial(t *testing.T) {
	manager, st := newTestGateway(t)
	server := newTestSSHServer(t)
	asset, err := st.CreateAsset(context.Background(), store.CreateAsset{AssetCode: "HK-2", Name: "hk", Address: "127.0.0.1", SSHPort: server.port(t), Username: "ops", AuthType: "password", Secret: "correct-horse"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := manager.Diagnose(context.Background(), asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.OK || lastStage(d).Stage != "host_key" {
		t.Fatalf("unapproved host key must stop at host_key stage, got %#v", d.Stages)
	}
}

func lastStage(d Diagnostic) Stage {
	if len(d.Stages) == 0 {
		return Stage{}
	}
	return d.Stages[len(d.Stages)-1]
}

func stageByName(d Diagnostic, name string) Stage {
	for _, stage := range d.Stages {
		if stage.Stage == name {
			return stage
		}
	}
	return Stage{}
}
