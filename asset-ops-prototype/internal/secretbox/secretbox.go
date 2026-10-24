package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Box struct{ aead cipher.AEAD }

func LoadOrCreate(path string) (*Box, error) {
	if path == "" {
		return nil, errors.New("master key path is required")
	}
	encoded, err := os.ReadFile(path)
	var decoded []byte
	if errors.Is(err, os.ErrNotExist) {
		decoded = make([]byte, 32)
		if _, err = rand.Read(decoded); err != nil {
			return nil, fmt.Errorf("generate master key: %w", err)
		}
		if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create key directory: %w", err)
		}
		if err = os.WriteFile(path, []byte(base64.RawStdEncoding.EncodeToString(decoded)), 0o600); err != nil {
			return nil, fmt.Errorf("write master key: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("read master key: %w", err)
	} else {
		decoded, err = base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
		if err != nil || len(decoded) != 32 {
			return nil, errors.New("master key must be a base64 encoded 32-byte value")
		}
	}
	block, err := aes.NewCipher(decoded)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return &Box{aead: aead}, nil
}

func (b *Box) Encrypt(plaintext []byte, aad string) (nonce, ciphertext []byte, err error) {
	nonce = make([]byte, b.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = b.aead.Seal(nil, nonce, plaintext, []byte(aad))
	return nonce, ciphertext, nil
}

func (b *Box) Decrypt(nonce, ciphertext []byte, aad string) ([]byte, error) {
	if len(nonce) != b.aead.NonceSize() {
		return nil, errors.New("invalid credential nonce")
	}
	plain, err := b.aead.Open(nil, nonce, ciphertext, []byte(aad))
	if err != nil {
		return nil, errors.New("credential authentication failed")
	}
	return plain, nil
}
