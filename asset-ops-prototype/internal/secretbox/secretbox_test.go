package secretbox

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestEncryptDecryptAndAAD(t *testing.T) {
	box, err := LoadOrCreate(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("ssh-private-secret")
	nonce, ciphertext, err := box.Encrypt(plain, "credential:v1")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, plain) {
		t.Fatal("ciphertext contains plaintext")
	}
	got, err := box.Decrypt(nonce, ciphertext, "credential:v1")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("got %q", got)
	}
	if _, err := box.Decrypt(nonce, ciphertext, "other-record"); err == nil {
		t.Fatal("AAD mismatch must fail")
	}
}
