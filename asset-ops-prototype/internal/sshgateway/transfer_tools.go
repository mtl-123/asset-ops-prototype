package sshgateway

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const toolVersion = "1.2.0"
const maxToolArchive = 16 << 20

// 官方 v1.2.0 checksums.txt；升级必须同时审核版本及校验值。
var toolHashes = map[string]string{
	"x86_64":  "70e3e0847177d4c7b681a8ec19fa00092e422a6c628ef9d8a5db6dfbf4612add",
	"aarch64": "9a73c237b6b12af267e878591ff22a01c97ca9d1cd8125f9ff4ffd6df4fea97c",
}
var toolCacheLock = make(chan struct{}, 1)

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

// 只追加到 PATH 尾部，已有用户命令优先，不替换任何已有安装。
const toolPath = `case ":$PATH:" in *":$HOME/.local/share/qiyun-trzsz/1.2.0/bin:"*) ;; *) PATH="$PATH:$HOME/.local/share/qiyun-trzsz/1.2.0/bin" ;; esac
export PATH`

const toolProbe = `set -eu
[ "$(uname -s)" = Linux ] || { echo '仅支持 Linux'; exit 1; }
case "${SHELL##*/}" in bash|zsh|sh|dash) ;; *) echo '仅支持 bash/zsh/sh/dash 的 PATH 配置'; exit 1 ;; esac
` + toolPath + `
if command -v trz >/dev/null 2>&1 && command -v tsz >/dev/null 2>&1; then echo QIYUN_READY; else uname -m; fi
`

const toolInstall = `set -eu
umask 077
[ -n "$HOME" ] && [ -d "$HOME" ] || { echo 'HOME 无效'; exit 1; }
base="$HOME/.local/share/qiyun-trzsz"
for p in "$HOME/.local" "$HOME/.local/share" "$base"; do
 [ ! -L "$p" ] || { echo '安装目录不能是符号链接'; exit 1; }
 mkdir -p "$p"
done
mkdir "$base/install.lock" 2>/dev/null || { echo '另一个安装正在进行，或遗留 install.lock；请检查后重试'; exit 1; }
stage=''
trap 'test -z "$stage" || rm -rf "$stage"; rmdir "$base/install.lock"' EXIT
trap 'exit 1' HUP INT TERM
` + toolPath + `
if ! command -v trz >/dev/null 2>&1 || ! command -v tsz >/dev/null 2>&1; then
 [ ! -e "$base/1.2.0" ] && [ ! -L "$base/1.2.0" ] || { echo '专有安装目录已存在但命令不可用；为避免覆盖，请人工检查'; exit 1; }
 stage=$(mktemp -d "$base/stage.XXXXXXXX")
 mkdir "$stage/bin"
 tar -xf - -C "$stage/bin"
 chmod 700 "$stage/bin/trz" "$stage/bin/tsz"
 "$stage/bin/trz" --version >/dev/null
 "$stage/bin/tsz" --version >/dev/null
 mv "$stage" "$base/1.2.0"
 stage=''
fi
# 在同一远程用户锁内写入幂等块；不跟随用户配置符号链接。
for rc in .profile .bashrc .bash_profile .bash_login .zshrc .zprofile; do
 f="$HOME/$rc"
 # 不新建优先级更高的 bash 登录文件，以免遮蔽用户已有配置。
 case "$rc" in .bash_profile|.bash_login) [ -e "$f" ] || continue ;; esac
 [ ! -L "$f" ] || { echo "PATH 配置拒绝符号链接：$rc"; exit 1; }
 [ ! -e "$f" ] || [ -f "$f" ] || { echo "PATH 配置不是普通文件：$rc"; exit 1; }
 if ! grep -Fqx '# BEGIN QIYUN TRZSZ PATH v1' "$f" 2>/dev/null; then
  printf '\n%s\n' '# BEGIN QIYUN TRZSZ PATH v1' >> "$f"
  printf '%s\n' '` + toolPath + `' '# END QIYUN TRZSZ PATH v1' >> "$f"
 fi
done
command -v trz >/dev/null && command -v tsz >/dev/null
echo QIYUN_READY
`

// 输出截断但持续消费，防止远程 banner 或异常输出占满网关内存。
type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if left := 8192 - b.Len(); left > 0 {
		if len(p) > left {
			p = p[:left]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

func runToolCommand(ctx context.Context, client *ssh.Client, script string, input io.Reader) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	session.Stdin = input
	var out boundedOutput
	session.Stdout = &out
	session.Stderr = &out
	done := make(chan error, 1)
	go func() { done <- session.Run("sh -c " + shellQuote(script)) }()
	select {
	case err = <-done:
		if err != nil {
			return "", fmt.Errorf("远程准备失败：%w；%s", err, strings.TrimSpace(out.String()))
		}
		return strings.TrimSpace(out.String()), nil
	case <-ctx.Done():
		return "", fmt.Errorf("远程准备超时或取消：%w", ctx.Err())
	}
}

func verifiedToolArchive(ctx context.Context, arch string) (*os.File, error) {
	hash, ok := toolHashes[arch]
	if !ok {
		return nil, fmt.Errorf("不支持 CPU 架构 %q，仅支持 amd64/x86_64 和 arm64/aarch64", arch)
	}
	select {
	case toolCacheLock <- struct{}{}:
		defer func() { <-toolCacheLock }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	root = filepath.Join(root, "qiyun", "trzsz", toolVersion)
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	name := "trzsz_" + toolVersion + "_linux_" + arch + ".tar.gz"
	dest := filepath.Join(root, name)
	if f, e := os.Open(dest); e == nil {
		if verifyToolHash(f, hash) == nil {
			_, err = f.Seek(0, 0)
			return f, err
		}
		f.Close()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://github.com/trzsz/trzsz-go/releases/download/v"+toolVersion+"/"+name, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) > 5 {
			return fmt.Errorf("发布包重定向不安全或过多")
		}
		return nil
	}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载官方工具包：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("官方工具包下载 HTTP %d", resp.StatusCode)
	}
	f, err := os.CreateTemp(root, "download-*")
	if err != nil {
		return nil, err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxToolArchive+1))
	if err != nil {
		return nil, err
	}
	if n > maxToolArchive {
		return nil, fmt.Errorf("发布包超过 16 MiB 限制")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return nil, err
	}
	if err = verifyToolHash(f, hash); err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	// Windows 不允许替换打开的文件；无效旧缓存只在校验新包成功后清理。
	if err = os.Remove(dest); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err = os.Rename(tmp, dest); err != nil {
		return nil, err
	}
	return os.Open(dest)
}

func verifyToolHash(r io.Reader, expected string) error {
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(r, maxToolArchive+1))
	if err != nil {
		return err
	}
	if n > maxToolArchive || fmt.Sprintf("%x", h.Sum(nil)) != expected {
		return fmt.Errorf("官方工具包 SHA-256 校验失败")
	}
	return nil
}

// 只把经过校验包中的两个普通二进制文件重打包；不向远端发送路径、链接或其他条目。
func toolPayload(r io.Reader) (*os.File, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	f, err := os.CreateTemp("", "qiyun-trzsz-*.tar")
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	tw := tar.NewWriter(f)
	tr := tar.NewReader(io.LimitReader(gz, 64<<20))
	seen := map[string]bool{}
	for {
		hdr, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		name := path.Base(hdr.Name)
		if name != "trz" && name != "tsz" {
			continue
		}
		if seen[name] || hdr.Typeflag != tar.TypeReg || hdr.Size <= 0 || hdr.Size > 24<<20 {
			return nil, fmt.Errorf("发布包二进制条目不合法：%s", name)
		}
		seen[name] = true
		if err = tw.WriteHeader(&tar.Header{Name: name, Mode: 0700, Size: hdr.Size, Typeflag: tar.TypeReg}); err != nil {
			return nil, err
		}
		if _, err = io.CopyN(tw, tr, hdr.Size); err != nil {
			return nil, err
		}
	}
	if !seen["trz"] || !seen["tsz"] {
		return nil, fmt.Errorf("发布包缺少 trz 或 tsz")
	}
	if err = tw.Close(); err != nil {
		return nil, err
	}
	if _, err = f.Seek(0, 0); err != nil {
		return nil, err
	}
	success = true
	return f, nil
}

func prepareTransferTools(ctx context.Context, client *ssh.Client) error {
	probe, err := runToolCommand(ctx, client, toolProbe, nil)
	if err != nil {
		return err
	}
	if probe == "QIYUN_READY" {
		// 自有目录仍需修复上次未完成的 PATH 配置；系统已有两命令则完全跳过。
		_, err = runToolCommand(ctx, client, `if command -v trz >/dev/null 2>&1 && command -v tsz >/dev/null 2>&1; then exit 0; fi
`+toolInstall, nil)
		return err
	}
	arch := strings.TrimSpace(probe)
	switch arch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	}
	archive, err := verifiedToolArchive(ctx, arch)
	if err != nil {
		return err
	}
	defer archive.Close()
	payload, err := toolPayload(archive)
	if err != nil {
		return err
	}
	defer os.Remove(payload.Name())
	defer payload.Close()
	_, err = runToolCommand(ctx, client, toolInstall, payload)
	return err
}
