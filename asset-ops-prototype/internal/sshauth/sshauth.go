package sshauth

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Auth kinds stored in the credentials table.
const (
	KindPassword   = "password"
	KindPrivateKey = "private_key"
)

// Clean normalises clipboard or file content without changing key material.
// Files saved on Windows often use CRLF and may miss the trailing newline that
// OpenSSH requires, which used to surface as a confusing parse error.
func Clean(secret string) string {
	s := strings.ReplaceAll(secret, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "PRIVATE KEY") && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s
}

// ValidatePassword rejects the empty and wrong-type secrets that otherwise fail
// much later as an opaque authentication error.
func ValidatePassword(secret string) error {
	if strings.TrimSpace(secret) == "" {
		return errors.New("请填写 SSH 密码")
	}
	if strings.Contains(secret, "PRIVATE KEY") {
		return errors.New("认证方式选择的是密码，但内容看起来是私钥；请切换为“SSH 私钥”")
	}
	return nil
}

// ParsePrivateKey converts the stored secret into a signer and explains precisely
// why parsing failed, so the operator does not have to guess.
func ParsePrivateKey(secret, passphrase string) (ssh.Signer, error) {
	cleaned := Clean(secret)
	if cleaned == "" {
		return nil, errors.New("请提供 OpenSSH 私钥内容，或从本地选择密钥文件")
	}
	if strings.Contains(cleaned, "PuTTY-User-Key-File") {
		return nil, errors.New("这是 PuTTY 的 .ppk 私钥，不受支持；请用 puttygen 转换为 OpenSSH 格式")
	}
	prefix := cleaned
	if len(prefix) > 32 {
		prefix = prefix[:32]
	}
	if strings.HasPrefix(prefix, "ssh-rsa ") || strings.HasPrefix(prefix, "ssh-ed25519 ") || strings.HasPrefix(prefix, "ecdsa-") {
		return nil, errors.New("这是公钥而不是私钥；请选择以 BEGIN ... PRIVATE KEY 开头的私钥文件")
	}
	if !strings.Contains(cleaned, "PRIVATE KEY") {
		return nil, errors.New("内容不是 OpenSSH/PEM 私钥；私钥应以 -----BEGIN OPENSSH PRIVATE KEY----- 开头")
	}
	if strings.Contains(cleaned, "ENCRYPTED") && strings.TrimSpace(passphrase) == "" {
		return nil, errors.New("私钥已加密，请填写私钥口令")
	}
	var signer ssh.Signer
	var err error
	if strings.TrimSpace(passphrase) != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(cleaned), []byte(passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey([]byte(cleaned))
	}
	if err == nil {
		return signer, nil
	}
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		return nil, errors.New("私钥已加密，需要正确的私钥口令")
	}
	if strings.Contains(err.Error(), "incorrect passphrase") || strings.Contains(err.Error(), "decryption password incorrect") {
		return nil, errors.New("私钥口令不正确")
	}
	if strings.Contains(err.Error(), "no key found") {
		return nil, errors.New("未能在内容中找到可用私钥；请确认文件完整，且不是 .pub 公钥或 .ppk 文件")
	}
	return nil, errors.New("私钥解析失败：" + err.Error())
}

// FingerprintSHA256 helps an operator compare the uploaded key against the
// target's ~/.ssh/authorized_keys, which is the usual reason authentication
// cannot match.
func FingerprintSHA256(key ssh.PublicKey) string {
	return ssh.FingerprintSHA256(key)
}

// AuthorizedKeyLine renders the key in authorized_keys form for comparison and
// for display in the UI.
func AuthorizedKeyLine(signer ssh.Signer) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
}

// ShortDigest is used only for correlating log lines, never as a substitute for
// the SHA-256 fingerprint shown to operators.
func ShortDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(sum[:6])
}
