// Package rdp launches the operator's own Windows Remote Desktop client for
// assets registered in the Go asset store.
//
// It carries over the launch chain from the web-vpn prototype: a temporary
// credential is written to the Windows Credential Manager, a temporary .rdp
// file is signed with the trusted "Pulse RDP Launcher" publisher certificate,
// mstsc.exe is started with that file, and the credential plus the file are
// removed after a short delay. Passwords never enter the .rdp file, the
// command line, the audit log or the browser.
//
// The service can only launch mstsc on the machine it runs on, so the HTTP
// layer additionally rejects non-loopback callers. A central server must not
// pop a desktop window on behalf of a remote browser.
package rdp

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"qiyun-asset-ops/internal/sshauth"
	"qiyun-asset-ops/internal/store"
)

//go:embed launcher/launch-rdp.ps1
var launchScript []byte

//go:embed launcher/setup-rdp-signing.ps1
var signingScript []byte

// AssetSource resolves an asset together with its decrypted credential. The
// store implements it; tests inject a fake instead of a real database.
type AssetSource interface {
	CredentialForAsset(ctx context.Context, id string) (store.Asset, store.Credential, error)
}

// Auditor records launch and signing attempts. Passwords must never be part of
// the metadata.
type Auditor interface {
	Audit(ctx context.Context, action, objectType, objectID, result string, metadata any)
}

// Call is one PowerShell invocation. FirstJSON makes the executor return as
// soon as the first JSON object line appears, because the launcher keeps the
// process alive for the delayed credential/file cleanup.
type Call struct {
	Script    string
	Args      []string
	Stdin     string
	FirstJSON bool
}

// Output is the captured result of a PowerShell invocation.
type Output struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Executor runs a PowerShell script. The default implementation uses
// powershell.exe with a hidden window; tests replace it with a fake.
type Executor interface {
	Run(ctx context.Context, call Call) (Output, error)
}

// Options tunes the manager.
type Options struct {
	DataDir string
	// Platform defaults to runtime.GOOS. Tests use it to simulate Windows.
	Platform string
	// PowerShell overrides the default System32 path.
	PowerShell string
	// Executor overrides the real process runner.
	Executor Executor
	// LaunchTimeout bounds the wait for the launcher's first JSON line.
	LaunchTimeout time.Duration
	// SetupTimeout bounds signing status/install (UAC may need operator input).
	SetupTimeout time.Duration
}

// Manager owns the extracted launcher scripts and the launch flow.
type Manager struct {
	assets   AssetSource
	audit    Auditor
	exec     Executor
	platform string
	dir      string

	launchPath  string
	signingPath string

	launchTimeout time.Duration
	setupTimeout  time.Duration

	mu     sync.Mutex
	caps   *Capability
	capsAt time.Time
	now    func() time.Time
}

// Profile is the validated launch request handed to launch-rdp.ps1.
type Profile struct {
	Address           string `json:"address"`
	Port              int    `json:"port"`
	Username          string `json:"username"`
	Password          string `json:"password"`
	RedirectClipboard bool   `json:"redirectClipboard"`
	RedirectDrives    bool   `json:"redirectDrives"`
	DriveMode         string `json:"driveMode"`
	DriveList         string `json:"driveList"`
	Fullscreen        bool   `json:"fullscreen"`
	MultiMonitor      bool   `json:"multiMonitor"`
}

// LaunchInput is what the browser may control; address, account and password
// always come from the asset store.
type LaunchInput struct {
	Port              int    `json:"port"`
	RedirectClipboard bool   `json:"redirect_clipboard"`
	RedirectDrives    bool   `json:"redirect_drives"`
	DriveMode         string `json:"drive_mode"`
	DriveList         string `json:"drive_list"`
	Fullscreen        bool   `json:"fullscreen"`
	MultiMonitor      bool   `json:"multi_monitor"`
	PromptCredentials bool   `json:"prompt_credentials"`
}

// LaunchResult mirrors the launcher's JSON status line.
type LaunchResult struct {
	OK     bool   `json:"ok"`
	PID    int    `json:"pid,omitempty"`
	Target string `json:"target"`
	Code   string `json:"code,omitempty"`
	Error  string `json:"error,omitempty"`
}

// SigningStatus mirrors setup-rdp-signing.ps1's status object. Tags are
// camelCase because the PowerShell script emits camelCase keys; pretending
// otherwise silently leaves the trust booleans false.
type SigningStatus struct {
	OK                    bool   `json:"ok"`
	Configured            bool   `json:"configured"`
	Reason                string `json:"reason,omitempty"`
	Publisher             string `json:"publisher,omitempty"`
	CertificateThumbprint string `json:"certificateThumbprint,omitempty"`
	PolicyThumbprint      string `json:"policyThumbprint,omitempty"`
	SigningHash           string `json:"signingHash,omitempty"`
	RootTrusted           bool   `json:"rootTrusted"`
	PublisherTrusted      bool   `json:"publisherTrusted"`
	PolicyConfigured      bool   `json:"policyConfigured"`
	Expires               string `json:"expires,omitempty"`
	Error                 string `json:"error,omitempty"`
}

// Capability describes what this machine can actually do. It is intentionally
// split: running the launcher, having mstsc/rdpsign, and having trusted
// signing configured are different facts.
type Capability struct {
	Platform      string        `json:"platform"`
	Available     bool          `json:"available"`
	MSTSC         bool          `json:"mstsc"`
	RDPSign       bool          `json:"rdpsign"`
	CredentialAPI bool          `json:"credential_api"`
	Signing       SigningStatus `json:"signing"`
	Error         string        `json:"error,omitempty"`
	CheckedAt     time.Time     `json:"checked_at"`
}

// RDPError is a browser-safe failure with a stable code and HTTP status.
type RDPError struct {
	Code    string
	Status  int
	Message string
}

func (e *RDPError) Error() string { return e.Message }

func fail(code string, status int, format string, args ...any) *RDPError {
	return &RDPError{Code: code, Status: status, Message: fmt.Sprintf(format, args...)}
}

// New prepares the launcher scripts under DataDir and returns a manager. The
// scripts are extracted to a stable path because the signing setup script
// re-launches itself through UAC using $PSCommandPath.
func New(assets AssetSource, audit Auditor, opts Options) (*Manager, error) {
	if assets == nil {
		return nil, errors.New("rdp: asset source is required")
	}
	if strings.TrimSpace(opts.DataDir) == "" {
		return nil, errors.New("rdp: data directory is required")
	}
	platform := opts.Platform
	if platform == "" {
		platform = runtime.GOOS
	}
	if opts.LaunchTimeout <= 0 {
		opts.LaunchTimeout = 20 * time.Second
	}
	if opts.SetupTimeout <= 0 {
		opts.SetupTimeout = 120 * time.Second
	}
	dir := filepath.Join(opts.DataDir, "rdp")
	m := &Manager{
		assets: assets, audit: audit, platform: platform, dir: dir,
		launchPath:    filepath.Join(dir, "launch-rdp.ps1"),
		signingPath:   filepath.Join(dir, "setup-rdp-signing.ps1"),
		launchTimeout: opts.LaunchTimeout, setupTimeout: opts.SetupTimeout,
		now: time.Now,
	}
	if opts.Executor != nil {
		m.exec = opts.Executor
	} else {
		m.exec = &processExecutor{powershell: powerShellPath(opts.PowerShell)}
	}
	// Only Windows ever executes these scripts; extracting them elsewhere would
	// write files nobody can use and make a read-only data dir fatal on Linux.
	if platform == "windows" {
		if err := m.extract(); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// NewLocal prepares a manager without an asset store. The central thin
// connector uses it to launch a profile that was redeemed from the server; the
// server remains the only component that reads asset credentials.
func NewLocal(opts Options) (*Manager, error) {
	if strings.TrimSpace(opts.DataDir) == "" {
		return nil, errors.New("rdp: data directory is required")
	}
	platform := opts.Platform
	if platform == "" {
		platform = runtime.GOOS
	}
	if opts.LaunchTimeout <= 0 {
		opts.LaunchTimeout = 20 * time.Second
	}
	if opts.SetupTimeout <= 0 {
		opts.SetupTimeout = 120 * time.Second
	}
	dir := filepath.Join(opts.DataDir, "rdp")
	m := &Manager{
		platform: platform, dir: dir,
		launchPath:    filepath.Join(dir, "launch-rdp.ps1"),
		signingPath:   filepath.Join(dir, "setup-rdp-signing.ps1"),
		launchTimeout: opts.LaunchTimeout, setupTimeout: opts.SetupTimeout,
		now: time.Now,
	}
	if opts.Executor != nil {
		m.exec = opts.Executor
	} else {
		m.exec = &processExecutor{powershell: powerShellPath(opts.PowerShell)}
	}
	if platform == "windows" {
		if err := m.extract(); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Manager) extract() error {
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return fmt.Errorf("rdp: create launcher directory: %w", err)
	}
	for name, body := range map[string][]byte{
		"launch-rdp.ps1":        launchScript,
		"setup-rdp-signing.ps1": signingScript,
	} {
		if err := os.WriteFile(filepath.Join(m.dir, name), body, 0o600); err != nil {
			return fmt.Errorf("rdp: write %s: %w", name, err)
		}
	}
	return nil
}

func powerShellPath(override string) string {
	if strings.TrimSpace(override) != "" {
		return override
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
}

// Capabilities reports the local launcher state, cached briefly so opening the
// RDP dialog repeatedly does not spawn PowerShell on every click.
func (m *Manager) Capabilities(ctx context.Context) Capability {
	if m.platform != "windows" {
		return Capability{
			Platform: m.platform, CheckedAt: m.now().UTC(),
			Error: "本服务不在 Windows 上运行，无法启动本机 mstsc。请在需要打开远程桌面的 Windows 电脑上运行服务。",
		}
	}
	m.mu.Lock()
	if m.caps != nil && m.now().Sub(m.capsAt) < 30*time.Second {
		cached := *m.caps
		m.mu.Unlock()
		return cached
	}
	m.mu.Unlock()

	cap := Capability{Platform: m.platform, CheckedAt: m.now().UTC()}
	self, err := m.selfTest(ctx)
	if err != nil {
		cap.Error = err.Error()
	} else {
		cap.MSTSC = self.MSTSC
		cap.RDPSign = self.RDPSign
		cap.CredentialAPI = self.CredentialAPI
		cap.Available = self.MSTSC && self.RDPSign && self.CredentialAPI
	}
	status, err := m.SigningStatus(ctx)
	if err != nil {
		cap.Signing = SigningStatus{Error: err.Error()}
		if cap.Error == "" {
			cap.Error = err.Error()
		}
	} else {
		cap.Signing = status
	}

	m.mu.Lock()
	m.caps, m.capsAt = &cap, m.now()
	m.mu.Unlock()
	return cap
}

type selfTestResult struct {
	OK            bool `json:"ok"`
	CredentialAPI bool `json:"credentialApi"`
	MSTSC         bool `json:"mstsc"`
	RDPSign       bool `json:"rdpsign"`
}

func (m *Manager) selfTest(ctx context.Context) (selfTestResult, error) {
	ctx, cancel := context.WithTimeout(ctx, m.launchTimeout)
	defer cancel()
	out, err := m.exec.Run(ctx, Call{Script: m.launchPath, Args: []string{"-SelfTest"}, FirstJSON: true})
	if err != nil {
		return selfTestResult{}, fmt.Errorf("无法运行 RDP 启动器：%w", err)
	}
	if out.ExitCode != 0 {
		return selfTestResult{}, fmt.Errorf("RDP 启动器自检失败（退出码 %d）：%s", out.ExitCode, strings.TrimSpace(out.Stderr))
	}
	var result selfTestResult
	if !decodeFirstJSON(out.Stdout, &result) {
		return selfTestResult{}, errors.New("RDP 启动器自检没有返回可识别的结果")
	}
	return result, nil
}

// SigningStatus runs the setup script in Status mode. It never changes system
// state; only SetupSigning does that, behind an explicit operator action.
func (m *Manager) SigningStatus(ctx context.Context) (SigningStatus, error) {
	if m.platform != "windows" {
		return SigningStatus{}, errors.New("RDP 签名状态只能在 Windows 上读取")
	}
	ctx, cancel := context.WithTimeout(ctx, m.setupTimeout)
	defer cancel()
	out, err := m.exec.Run(ctx, Call{Script: m.signingPath, Args: []string{"-Mode", "Status"}, FirstJSON: true})
	if err != nil {
		return SigningStatus{}, fmt.Errorf("读取 RDP 签名状态失败：%w", err)
	}
	if out.ExitCode != 0 {
		return SigningStatus{}, fmt.Errorf("读取 RDP 签名状态失败（退出码 %d）：%s", out.ExitCode, strings.TrimSpace(out.Stderr))
	}
	var status SigningStatus
	if !decodeFirstJSON(out.Stdout, &status) {
		return SigningStatus{}, errors.New("RDP 签名脚本没有返回可识别的结果")
	}
	if !status.OK && status.Reason == "" {
		status.Reason = "签名状态不可用"
	}
	return status, nil
}

// SetupSigning installs the per-user publisher certificate and machine policy.
// It can trigger one UAC prompt; the caller must present that consequence to
// the operator before calling.
func (m *Manager) SetupSigning(ctx context.Context) (SigningStatus, error) {
	if m.platform != "windows" {
		return SigningStatus{}, fail("RDP_PLATFORM_UNSUPPORTED", 501, "本服务不在 Windows 上运行，无法配置 RDP 签名")
	}
	// Installation may partially complete (e.g. a certificate is created before
	// the user cancels policy approval). Never retain the pre-install cache.
	defer func() {
		m.mu.Lock()
		m.caps = nil
		m.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, m.setupTimeout)
	defer cancel()
	out, runErr := m.exec.Run(ctx, Call{Script: m.signingPath, Args: []string{"-Mode", "Install"}})
	var status SigningStatus
	var setupErr *RDPError
	switch {
	case runErr != nil || out.ExitCode != 0:
		setupErr = signingSetupFailure(out, runErr)
	case !decodeLastJSON(out.Stdout, &status):
		setupErr = fail("RDP_SIGNING_INVALID_RESULT", 502, "签名初始化未返回有效状态，请重新检测。")
	case !status.OK || !status.Configured:
		setupErr = fail("RDP_SIGNING_INCOMPLETE", 409, "签名初始化尚未完成，请核对证书信任和发布者策略提示后重新检测。")
	}
	if setupErr != nil {
		// Keep script diagnostics in server logs; don't fill the modal with a
		// PowerShell stack or disclose local paths in the public error message.
		slog.Warn("rdp signing setup failed", "code", setupErr.Code, "exit_code", out.ExitCode, "error", runErr, "stderr", strings.TrimSpace(out.Stderr))
		if m.audit != nil {
			m.audit.Audit(context.WithoutCancel(ctx), "rdp.signing.setup", "rdp", "local", "failed", map[string]any{"code": setupErr.Code, "exit_code": out.ExitCode})
		}
		return status, setupErr
	}
	if m.audit != nil {
		m.audit.Audit(ctx, "rdp.signing.setup", "rdp", "local", "success", map[string]any{"configured": status.Configured, "signing_hash": status.SigningHash})
	}
	return status, nil
}

// signingSetupFailure classifies failures without changing certificates or
// security policy. NTE_PERM describes local key-store access, not bad RDP login.
func signingSetupFailure(out Output, runErr error) *RDPError {
	if errors.Is(runErr, context.DeadlineExceeded) {
		return fail("RDP_SIGNING_TIMEOUT", 504, "签名初始化等待超时，请查看 Windows 证书或 UAC 提示，完成后重新检测。")
	}
	if errors.Is(runErr, context.Canceled) {
		return fail("RDP_SIGNING_CANCELLED", 409, "签名初始化已取消，请重新检测当前状态。")
	}
	diagnostic := strings.ToLower(out.Stderr + "\n" + out.Stdout)
	if runErr != nil {
		diagnostic += "\n" + strings.ToLower(runErr.Error())
	}
	for _, marker := range []string{"0x80090010", "nte_perm", "0x80070005", "access is denied", "access denied", "拒绝访问"} {
		if strings.Contains(diagnostic, marker) {
			return fail("RDP_SIGNING_ACCESS_DENIED", 403, "当前服务无法写入 Windows 证书/私钥存储。请从本机普通 Windows 终端运行 start-qiyun.cmd 后重试。")
		}
	}
	return fail("RDP_SIGNING_SETUP_FAILED", 502, "签名初始化失败，请查看服务控制台中的诊断信息，完成 Windows 提示后重新检测。")
}

// Launch opens mstsc for one registered Windows asset.
func (m *Manager) Launch(ctx context.Context, assetID string, in LaunchInput) (LaunchResult, error) {
	profile, asset, err := m.profile(ctx, assetID, in)
	if err != nil {
		return LaunchResult{}, err
	}
	result, err := m.LaunchProfile(ctx, profile)
	if err != nil {
		m.auditLaunch(ctx, asset, "failed", map[string]any{"code": launchErrorCode(err)})
		return result, err
	}
	m.auditLaunch(ctx, asset, "success", map[string]any{"target": result.Target, "pid": result.PID})
	return result, nil
}

// LaunchProfile runs the embedded PowerShell launcher for an already resolved
// profile. It is used both by the local service and by the thin connector after
// it redeems a one-time ticket from the central server.
func (m *Manager) LaunchProfile(ctx context.Context, profile Profile) (LaunchResult, error) {
	if m.platform != "windows" {
		return LaunchResult{}, fail("RDP_PLATFORM_UNSUPPORTED", 501, "本机不是 Windows，无法启动 mstsc")
	}
	if err := Validate(profile); err != nil {
		return LaunchResult{}, err
	}
	payload, err := json.Marshal(profile)
	if err != nil {
		return LaunchResult{}, fail("RDP_LAUNCH_FAILED", 500, "无法序列化 RDP 连接参数")
	}
	ctx, cancel := context.WithTimeout(ctx, m.launchTimeout)
	defer cancel()
	// The moment the launcher prints its status line the desktop is open (or the
	// failure is final); waiting for the 30-second cleanup would make the button
	// feel broken. The executor keeps draining and reaping the child.
	out, err := m.exec.Run(ctx, Call{Script: m.launchPath, Stdin: string(payload), FirstJSON: true})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return LaunchResult{}, fail("RDP_LAUNCH_TIMEOUT", 504, "启动 mstsc 超时，请检查本机远程桌面客户端是否可用")
		}
		return LaunchResult{}, fail("RDP_LAUNCH_FAILED", 502, "启动 mstsc 失败：%v", err)
	}
	var result LaunchResult
	if !decodeFirstJSON(out.Stdout, &result) {
		msg := strings.TrimSpace(out.Stderr)
		if msg == "" && out.ExitCode != 0 {
			msg = fmt.Sprintf("退出码 %d", out.ExitCode)
		}
		if msg == "" {
			msg = "启动器没有返回结果"
		}
		return LaunchResult{}, fail("RDP_LAUNCH_FAILED", 502, "启动 mstsc 失败：%s", msg)
	}
	if !result.OK {
		if result.Code == "signing_setup_required" {
			return result, fail("RDP_SIGNING_REQUIRED", 409, "需要先启用受信任的 RDP 发布者签名，然后才能在无确认窗口的情况下打开远程桌面")
		}
		code := result.Code
		if code == "" {
			code = "RDP_LAUNCH_FAILED"
		}
		message := result.Error
		if message == "" {
			message = "启动 mstsc 失败"
		}
		return result, fail(code, 502, "%s", message)
	}
	return result, nil
}

func launchErrorCode(err error) string {
	var rdpErr *RDPError
	if errors.As(err, &rdpErr) {
		return rdpErr.Code
	}
	return "RDP_LAUNCH_FAILED"
}

func (m *Manager) auditLaunch(ctx context.Context, asset store.Asset, result string, metadata map[string]any) {
	if m.audit == nil {
		return
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["asset_code"] = asset.AssetCode
	metadata["address"] = asset.Address
	metadata["rdp_port"] = asset.RDPPort
	m.audit.Audit(ctx, "asset.rdp.launch", "asset", asset.ID, result, metadata)
}

// profile resolves everything the launcher needs. Anything the browser sends is
// only a resource preference: target, account and password are server-side.
func (m *Manager) profile(ctx context.Context, assetID string, in LaunchInput) (Profile, store.Asset, error) {
	asset, credential, err := m.assets.CredentialForAsset(ctx, assetID)
	if err != nil {
		if store.IsNotFound(err) {
			return Profile{}, asset, fail("RDP_ASSET_NOT_FOUND", 404, "资产不存在或已删除")
		}
		return Profile{}, asset, fail("RDP_LAUNCH_FAILED", 500, "读取资产凭据失败：%v", err)
	}
	if !asset.IsWindows() {
		return Profile{}, asset, fail("RDP_ASSET_NOT_WINDOWS", 400, "该资产不是 Windows 主机，不能发起 RDP 会话")
	}
	port := asset.RDPPort
	if in.Port > 0 {
		port = in.Port
	}
	if port == 0 {
		port = 3389
	}
	profile := Profile{
		Address: asset.Address, Port: port,
		RedirectClipboard: in.RedirectClipboard,
		RedirectDrives:    in.RedirectDrives,
		DriveMode:         "all",
		Fullscreen:        in.Fullscreen,
		MultiMonitor:      in.MultiMonitor,
	}
	if strings.EqualFold(strings.TrimSpace(in.DriveMode), "custom") {
		profile.DriveMode = "custom"
		profile.DriveList = in.DriveList
	}
	if !in.PromptCredentials {
		if credential.AuthType != sshauth.KindPassword {
			return Profile{}, asset, fail("RDP_PASSWORD_REQUIRED", 409, "该资产的凭据是 SSH 私钥，RDP 需要密码；请先更新凭据或改用交互式登录")
		}
		if strings.TrimSpace(credential.Secret) == "" {
			return Profile{}, asset, fail("RDP_PASSWORD_REQUIRED", 409, "该资产没有可用的密码凭据，请先更新凭据或改用交互式登录")
		}
		profile.Username = credential.Username
		profile.Password = credential.Secret
	}
	if err := Validate(profile); err != nil {
		var rdpErr *RDPError
		if errors.As(err, &rdpErr) {
			return Profile{}, asset, rdpErr
		}
		return Profile{}, asset, fail("RDP_LAUNCH_FAILED", 400, "%v", err)
	}
	return profile, asset, nil
}

// Validate applies the same constraints as the prototype server: no control
// characters that could corrupt the .rdp file, a real port, a sane drive list.
func Validate(p Profile) error {
	if p.Address == "" || len(p.Address) > 255 || strings.ContainsAny(p.Address, " \t\r\n") {
		return fail("RDP_INVALID_TARGET", 400, "目标地址无效")
	}
	if p.Port < 1 || p.Port > 65535 {
		return fail("RDP_INVALID_PORT", 400, "RDP 端口必须在 1-65535 之间")
	}
	if strings.ContainsAny(p.Username, "\r\n") || len(p.Username) > 512 {
		return fail("RDP_INVALID_USERNAME", 400, "用户名无效或过长")
	}
	if len(p.Password) > 2048 {
		return fail("RDP_PASSWORD_TOO_LONG", 400, "密码过长")
	}
	if p.Password != "" && p.Username == "" {
		return fail("RDP_INVALID_USERNAME", 400, "填写密码时必须同时填写用户名")
	}
	if p.RedirectDrives && p.DriveMode == "custom" {
		drives := splitDrives(p.DriveList)
		if len(drives) == 0 {
			return fail("RDP_INVALID_DRIVES", 400, "自定义磁盘映射不能为空")
		}
		for _, drive := range drives {
			if len(drive) != 2 && len(drive) != 3 {
				return fail("RDP_INVALID_DRIVES", 400, "磁盘盘符格式无效：%s", drive)
			}
			letter := drive[0]
			if !((letter >= 'A' && letter <= 'Z') || (letter >= 'a' && letter <= 'z')) {
				return fail("RDP_INVALID_DRIVES", 400, "磁盘盘符格式无效：%s", drive)
			}
			if (len(drive) == 2 && drive[1] != ':') || (len(drive) == 3 && drive[2] != '\\') {
				return fail("RDP_INVALID_DRIVES", 400, "磁盘盘符格式无效：%s", drive)
			}
		}
	}
	return nil
}

func splitDrives(list string) []string {
	fields := strings.FieldsFunc(list, func(r rune) bool { return r == ';' || r == ',' })
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if value := strings.TrimSpace(field); value != "" {
			out = append(out, value)
		}
	}
	return out
}

// decodeFirstJSON finds the first complete JSON object in the output. Windows
// PowerShell may emit a BOM or blank lines, so strict whole-output parsing is
// not reliable.
func decodeFirstJSON(stdout string, v any) bool {
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || !strings.HasPrefix(line, "{") || !json.Valid([]byte(line)) {
			continue
		}
		if json.Unmarshal([]byte(line), v) == nil {
			return true
		}
	}
	return false
}

func decodeLastJSON(stdout string, v any) bool {
	lines := strings.Split(stdout, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(strings.TrimPrefix(lines[i], "\ufeff"))
		if line == "" || !strings.HasPrefix(line, "{") || !json.Valid([]byte(line)) {
			continue
		}
		if json.Unmarshal([]byte(line), v) == nil {
			return true
		}
	}
	return false
}

// processExecutor is the production Executor: a hidden PowerShell process with
// piped stdio.
type processExecutor struct {
	powershell string
}

func (e *processExecutor) Run(ctx context.Context, call Call) (Output, error) {
	args := append([]string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", call.Script}, call.Args...)
	// The launcher deliberately outlives the HTTP response: after printing its
	// status line it waits ~30 seconds and then deletes the temporary credential
	// and .rdp file. Binding the child to the request context killed that cleanup,
	// leaving TERMSRV/<host> in Credential Manager and pulse-rdp-*.rdp on disk.
	// The ctx below still bounds how long we wait for the first JSON line, and we
	// kill the process ourselves on timeout.
	processCtx := ctx
	if call.FirstJSON {
		processCtx = context.WithoutCancel(ctx)
	}
	cmd := exec.CommandContext(processCtx, e.powershell, args...)
	cmd.Stdin = strings.NewReader(call.Stdin)
	if !call.FirstJSON {
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		out := Output{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode(cmd, err)}
		if err != nil && ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, nil
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Output{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return Output{}, err
	}
	lines := make(chan string, 1)
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		reader := bufio.NewReader(stdout)
		sent := false
		for {
			line, readErr := reader.ReadString('\n')
			if !sent {
				trimmed := strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
				if trimmed != "" && strings.HasPrefix(trimmed, "{") && json.Valid([]byte(trimmed)) {
					lines <- trimmed
					sent = true
				}
			}
			if readErr != nil {
				break
			}
		}
		// Keep draining the pipes until the launcher finishes its delayed cleanup.
		_ = cmd.Wait()
	}()
	select {
	case line := <-lines:
		return Output{Stdout: line, ExitCode: 0}, nil
	case <-drained:
		select {
		case line := <-lines:
			return Output{Stdout: line}, nil
		default:
			return Output{Stdout: "", Stderr: stderr.String(), ExitCode: exitCode(cmd, nil)}, nil
		}
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		slog.Warn("rdp launcher timed out", "script", filepath.Base(call.Script))
		return Output{Stderr: stderr.String()}, ctx.Err()
	}
}

func exitCode(cmd *exec.Cmd, runErr error) int {
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode()
	}
	if runErr != nil {
		return -1
	}
	return 0
}
