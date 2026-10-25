package sshgateway

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolHash(t *testing.T) {
	data := []byte("official fixture")
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	if err := verifyToolHash(bytes.NewReader(data), hash); err != nil {
		t.Fatal(err)
	}
	if verifyToolHash(strings.NewReader("tampered"), hash) == nil {
		t.Fatal("接受了错误哈希")
	}
	if _, err := verifiedToolArchive(context.Background(), "mips"); err == nil {
		t.Fatal("接受了不支持架构")
	}
}

func archiveFixture(t *testing.T, names []string, kind byte) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, name := range names {
		data := []byte("#!/bin/sh\nexit 0\n")
		hdr := &tar.Header{Name: name, Mode: 0700, Typeflag: kind, Size: int64(len(data))}
		if kind != tar.TypeReg {
			hdr.Size = 0
			hdr.Linkname = "/tmp/target"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Size > 0 {
			if _, err := tw.Write(data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestToolPayload(t *testing.T) {
	for _, tc := range []struct {
		names []string
		kind  byte
		ok    bool
	}{
		{[]string{"release/trz", "release/tsz"}, tar.TypeReg, true},
		{[]string{"release/trz"}, tar.TypeReg, false},
		{[]string{"trz", "trz", "tsz"}, tar.TypeReg, false},
		{[]string{"trz", "tsz"}, tar.TypeSymlink, false},
	} {
		f, err := toolPayload(bytes.NewReader(archiveFixture(t, tc.names, tc.kind)))
		if (err == nil) != tc.ok {
			t.Fatalf("%v: %v", tc.names, err)
		}
		if err != nil {
			continue
		}
		tr := tar.NewReader(f)
		for _, want := range []string{"trz", "tsz"} {
			h, e := tr.Next()
			if e != nil || h.Name != want || h.Typeflag != tar.TypeReg {
				t.Fatalf("不安全条目 %v %v", h, e)
			}
		}
		f.Close()
		os.Remove(f.Name())
	}
}

// 只在临时 HOME 中执行安装脚本，不连接任何远程主机。
func TestToolInstallLocalSandbox(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("本机未提供 bash")
	}
	home := filepath.ToSlash(t.TempDir())
	run := func(script string, input io.Reader) (string, error) {
		cmd := exec.Command(bash, "--noprofile", "--norc", "-c", script)
		cmd.Env = append(os.Environ(), "HOME="+home, "SHELL=/bin/bash")
		cmd.Stdin = input
		b, e := cmd.CombinedOutput()
		return string(b), e
	}
	payload, err := toolPayload(bytes.NewReader(archiveFixture(t, []string{"trz", "tsz"}, tar.TypeReg)))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(payload.Name())
	defer payload.Close()
	// 保持系统命令可用，同时排除测试环境预装的 trz/tsz。
	script := "PATH=/usr/bin:/bin\n" + toolInstall
	if out, e := run(script, payload); e != nil {
		t.Fatalf("安装：%v %s", e, out)
	}
	if out, e := run(script, nil); e != nil {
		t.Fatalf("重复安装：%v %s", e, out)
	}
	for _, rc := range []string{".profile", ".bashrc", ".zshrc", ".zprofile"} {
		b, e := os.ReadFile(filepath.Join(home, rc))
		if e != nil {
			t.Fatal(e)
		}
		if strings.Count(string(b), "# BEGIN QIYUN TRZSZ PATH v1") != 1 {
			t.Fatalf("非幂等 PATH：%s", rc)
		}
	}
	if _, e := os.Stat(filepath.Join(home, ".bash_profile")); !os.IsNotExist(e) {
		t.Fatal("不应创建遮蔽 profile 的 bash_profile")
	}
	if out, e := run(toolPath+"\ncommand -v trz && command -v tsz", nil); e != nil {
		t.Fatalf("当前会话 PATH：%v %s", e, out)
	}
	// 已有用户命令优先；再次安装不覆盖用户文件。
	userBin := filepath.Join(home, "user-bin")
	if e := os.Mkdir(userBin, 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"trz", "tsz"} {
		if e := os.WriteFile(filepath.Join(userBin, name), []byte("#!/bin/sh\necho user-owned\n"), 0700); e != nil {
			t.Fatal(e)
		}
	}
	if out, e := run("PATH=\"$HOME/user-bin:/usr/bin:/bin\"\n"+toolPath+"\ntrz && tsz", nil); e != nil || strings.Count(out, "user-owned") != 2 {
		t.Fatalf("覆盖了用户命令：%v %s", e, out)
	}
	lock := filepath.Join(home, ".local/share/qiyun-trzsz/install.lock")
	if e := os.Mkdir(lock, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := run(script, nil); e == nil {
		t.Fatal("互斥锁未生效")
	}
}

func TestShellQuoteAndBoundedOutput(t *testing.T) {
	if shellQuote("a'b") != "'a'\"'\"'b'" {
		t.Fatal("shell 引用不正确")
	}
	var out boundedOutput
	n, err := out.Write(make([]byte, 1<<20))
	if err != nil || n != 1<<20 || out.Len() != 8192 {
		t.Fatal("输出没有限制大小")
	}
}
