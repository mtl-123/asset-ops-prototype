package sshauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func testKey(t *testing.T, passphrase string) (string, ssh.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "test")
	if err != nil {
		t.Fatal(err)
	}
	data := block.Bytes
	if passphrase != "" {
		encrypted, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "test", []byte(passphrase))
		if err != nil {
			t.Fatal(err)
		}
		data = encrypted.Bytes
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: block.Type, Bytes: data})), key
}

func TestParsePrivateKeyNormalisesWindowsLineEndings(t *testing.T) {
	key, _ := testKey(t, "")
	// A file saved on Windows has CRLF endings and often no trailing newline.
	windowsStyle := strings.TrimRight(strings.ReplaceAll(key, "\n", "\r\n"), "\r\n")
	signer, err := ParsePrivateKey(windowsStyle, "")
	if err != nil {
		t.Fatalf("CRLF key should parse: %v", err)
	}
	if !strings.HasPrefix(FingerprintSHA256(signer.PublicKey()), "SHA256:") {
		t.Fatal("expected SHA-256 fingerprint")
	}
}

func TestParsePrivateKeyExplainsWrongInput(t *testing.T) {
	_, pub := testKey(t, "")
	cases := []struct{ name, secret, want string }{
		{"empty", "", "私钥"},
		{"public key", strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))), "公钥"},
		{"ppk", "PuTTY-User-Key-File-3: ssh-ed25519", "ppk"},
		{"junk", "hello world", "OPENSSH PRIVATE KEY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePrivateKey(tc.secret, "")
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q should mention %q", err, tc.want)
			}
		})
	}
}

func TestParsePrivateKeyRequiresPassphrase(t *testing.T) {
	key, _ := testKey(t, "s3cret")
	if _, err := ParsePrivateKey(key, ""); err == nil || !strings.Contains(err.Error(), "口令") {
		t.Fatalf("expected passphrase requirement, got %v", err)
	}
	if _, err := ParsePrivateKey(key, "wrong"); err == nil || !strings.Contains(err.Error(), "口令不正确") {
		t.Fatalf("expected wrong passphrase error, got %v", err)
	}
	if _, err := ParsePrivateKey(key, "s3cret"); err != nil {
		t.Fatalf("correct passphrase should parse: %v", err)
	}
}

func TestValidatePasswordRejectsWrongTypes(t *testing.T) {
	if err := ValidatePassword("   "); err == nil {
		t.Fatal("empty password must fail")
	}
	key, _ := testKey(t, "")
	if err := ValidatePassword(key); err == nil || !strings.Contains(err.Error(), "私钥") {
		t.Fatalf("private key pasted as password must be explained, got %v", err)
	}
	if err := ValidatePassword("plain-password"); err != nil {
		t.Fatalf("valid password rejected: %v", err)
	}
}
