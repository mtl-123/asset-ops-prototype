//go:build windows

// Command launcher starts the local qiyun service when needed, opens the web
// console, and can run as a small login watchdog. It is built as a GUI
// subsystem binary so ordinary users never see a console window.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	listenAddress = "127.0.0.1:8765"
	healthAddress = "http://127.0.0.1:8765/healthz"
	defaultURL    = "http://127.0.0.1:8765/#/hosts"
	watchMutex    = `Local\QiyunAssetOpsWatchdog`
)

type options struct {
	exe, data, web, url, logPath string
	startOnly, watch, quiet      bool
	timeout                      time.Duration
}

func main() {
	appDir, err := executableDir()
	if err != nil {
		appDir = "."
	}
	opts := options{}
	flag.StringVar(&opts.exe, "exe", filepath.Join(appDir, "qiyun.exe"), "qiyun executable")
	flag.StringVar(&opts.data, "data", filepath.Join(appDir, "data"), "data directory")
	flag.StringVar(&opts.web, "web", appDir, "frontend directory")
	flag.StringVar(&opts.url, "url", defaultURL, "page to open")
	flag.StringVar(&opts.logPath, "log", filepath.Join(appDir, "logs", "launcher.log"), "launcher log file")
	flag.BoolVar(&opts.startOnly, "start-only", false, "start the service without opening the browser")
	flag.BoolVar(&opts.watch, "watch", false, "run as a login watchdog and restart the service if it stops")
	flag.BoolVar(&opts.quiet, "quiet", false, "do not show message boxes")
	flag.DurationVar(&opts.timeout, "timeout", 20*time.Second, "how long to wait for the service")
	flag.Parse()

	setupLog(opts.logPath)
	if opts.watch {
		runWatch(opts)
		return
	}
	if healthy(900 * time.Millisecond) {
		if !opts.startOnly {
			openBrowser(opts.url)
		}
		return
	}
	if portOpen(400 * time.Millisecond) {
		fail(opts.quiet, "栖云服务未就绪", "端口 8765 已被其他程序占用，但健康检查失败。\n请关闭占用该端口的程序后重试。")
	}
	if err := startQiyun(opts); err != nil {
		fail(opts.quiet, "栖云服务启动失败", err.Error())
	}
	if !waitHealthy(opts.timeout) {
		fail(opts.quiet, "栖云服务启动超时", "服务未在预期时间内就绪。\n请查看 logs\\qiyun.log 后重试。")
	}
	if !opts.startOnly {
		openBrowser(opts.url)
	}
}

func runWatch(opts options) {
	if !acquireWatchMutex() {
		log.Printf("watchdog already running")
		return
	}
	log.Printf("watchdog started")
	for {
		if healthy(2 * time.Second) {
			time.Sleep(10 * time.Second)
			continue
		}
		if portOpen(500 * time.Millisecond) {
			// Another process owns the port. Do not start a second instance or
			// kill an unknown service; just report it and retry later.
			log.Printf("port %s is open but /healthz is not ready", listenAddress)
			time.Sleep(10 * time.Second)
			continue
		}
		if err := startQiyun(opts); err != nil {
			log.Printf("watchdog could not start qiyun: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}
		if waitHealthy(30 * time.Second) {
			log.Printf("service became healthy")
		} else {
			log.Printf("service did not become healthy in time")
		}
		time.Sleep(5 * time.Second)
	}
}

func executableDir() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
}

func setupLog(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if info, err := os.Stat(path); err == nil && info.Size() > 5<<20 {
		_ = os.Rename(path, path+".1")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(file)
	log.SetFlags(log.LstdFlags)
}

func healthy(timeout time.Duration) bool {
	client := &http.Client{Timeout: timeout}
	response, err := client.Get(healthAddress)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	return strings.Contains(string(body), `"status":"ok"`)
}

func portOpen(timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", listenAddress, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func waitHealthy(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if healthy(time.Second) {
			return true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

func startQiyun(opts options) error {
	if _, err := os.Stat(opts.exe); err != nil {
		return fmt.Errorf("找不到程序：%s", opts.exe)
	}
	logDir := filepath.Dir(opts.logPath)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("无法创建日志目录：%w", err)
	}
	out, err := os.OpenFile(filepath.Join(logDir, "qiyun.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("无法写入 qiyun 日志：%w", err)
	}
	defer out.Close()
	cmd := exec.Command(opts.exe, "-addr", listenAddress, "-data", opts.data, "-web", opts.web)
	cmd.Dir = filepath.Dir(opts.exe)
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 qiyun.exe 失败：%w", err)
	}
	log.Printf("started qiyun pid=%d", cmd.Process.Pid)
	_ = cmd.Process.Release()
	return nil
}

func openBrowser(url string) {
	cmd := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if err := cmd.Start(); err != nil {
		log.Printf("open browser failed: %v", err)
		messageBox("无法打开浏览器", "请手动打开 "+url, 0x10)
	}
}

func acquireWatchMutex() bool {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	createMutex := kernel32.NewProc("CreateMutexW")
	name, err := syscall.UTF16PtrFromString(watchMutex)
	if err != nil {
		return false
	}
	handle, _, callErr := createMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return false
	}
	if callErr == syscall.Errno(183) { // ERROR_ALREADY_EXISTS
		_ = syscall.CloseHandle(syscall.Handle(handle))
		return false
	}
	return true
}

func messageBox(title, text string, flags uint) {
	user32 := syscall.NewLazyDLL("user32.dll")
	box := user32.NewProc("MessageBoxW")
	titlePtr, _ := syscall.UTF16PtrFromString(title)
	textPtr, _ := syscall.UTF16PtrFromString(text)
	_, _, _ = box.Call(0, uintptr(unsafe.Pointer(textPtr)), uintptr(unsafe.Pointer(titlePtr)), uintptr(flags))
}

func fail(quiet bool, title, text string) {
	log.Printf("%s: %s", title, text)
	if !quiet {
		messageBox(title, text, 0x10)
	}
	os.Exit(1)
}
