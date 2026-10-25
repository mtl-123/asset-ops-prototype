package sshgateway

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
	"qiyun-asset-ops/internal/sshauth"
	"qiyun-asset-ops/internal/store"
)

type Ticket struct {
	SessionID, AssetID, Value string
	Expires                   time.Time
	Used                      bool
	// Initial PTY size requested by the browser; applied before the shell starts.
	Cols, Rows int
}
type Manager struct {
	store   *store.Store
	allowed []netip.Prefix
	origins []string
	mu      sync.Mutex
	tickets map[string]*Ticket
}

func New(st *store.Store, cidrs, origins []string) (*Manager, error) {
	m := &Manager{store: st, tickets: map[string]*Ticket{}, origins: origins}
	if len(cidrs) == 0 {
		cidrs = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
	}
	for _, raw := range cidrs {
		p, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("invalid allowed CIDR %q: %w", raw, err)
		}
		m.allowed = append(m.allowed, p)
	}
	return m, nil
}

func (m *Manager) ResolveTarget(ctx context.Context, a store.Asset) (string, error) {
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", a.Address)
	if err != nil {
		return "", fmt.Errorf("resolve asset address: %w", err)
	}
	for _, ip := range ips {
		ip = ip.Unmap()
		for _, p := range m.allowed {
			if p.Contains(ip) {
				return net.JoinHostPort(ip.String(), strconv.Itoa(a.SSHPort)), nil
			}
		}
	}
	return "", errors.New("asset address resolves outside QIYUN_ALLOWED_CIDRS")
}

// ConnError carries an actionable hint alongside a connection failure so the API
// and the UI can explain what to fix instead of showing a bare syscall error.
type ConnError struct {
	Stage  string
	Detail string
	Hint   string
}

func (e *ConnError) Error() string { return e.Detail }

func newConnError(stage, detail, hint string) *ConnError {
	return &ConnError{Stage: stage, Detail: detail, Hint: hint}
}

func (m *Manager) ScanHostKey(ctx context.Context, a store.Asset) (key, fingerprint, target string, err error) {
	target, err = m.ResolveTarget(ctx, a)
	if err != nil {
		return "", "", "", newConnError("resolve", err.Error(), "目标地址必须能在本机网络内解析，并位于 QIYUN_ALLOWED_CIDRS 允许网段")
	}
	dialer := net.Dialer{Timeout: 8 * time.Second}
	conn, e := dialer.DialContext(ctx, "tcp", target)
	if e != nil {
		return "", "", target, newConnError("tcp", fmt.Sprintf("dial SSH %s: %v", target, e), tcpHint(e))
	}
	defer conn.Close()
	var captured ssh.PublicKey
	cfg := &ssh.ClientConfig{User: "invalid-probe-user", HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
		captured = k
		return errors.New("host key captured")
	}, Timeout: 8 * time.Second}
	_, _, _, e = ssh.NewClientConn(conn, target, cfg)
	if captured == nil {
		if e == nil {
			err = newConnError("handshake", "SSH 服务端没有提供主机公钥", "确认该端口上运行的是 SSH，而不是 HTTP 或其他协议")
		} else {
			err = newConnError("handshake", fmt.Sprintf("SSH 握手失败：%v", e), "端口可能不是 SSH，或 sshd 版本/算法不兼容")
		}
		return "", "", target, err
	}
	key = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(captured)))
	fingerprint = ssh.FingerprintSHA256(captured)
	return key, fingerprint, target, nil
}

func hostKeyCallback(expected string) (ssh.HostKeyCallback, error) {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(expected))
	if err != nil {
		return nil, errors.New("stored host key is invalid")
	}
	return func(_ string, _ net.Addr, actual ssh.PublicKey) error {
		if !bytesEqual(key.Marshal(), actual.Marshal()) || key.Type() != actual.Type() {
			return fmt.Errorf("HOST_KEY_MISMATCH: expected %s, received %s", ssh.FingerprintSHA256(key), ssh.FingerprintSHA256(actual))
		}
		return nil
	}, nil
}
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var x byte
	for i := range a {
		x |= a[i] ^ b[i]
	}
	return x == 0
}

func authMethods(c store.Credential) ([]ssh.AuthMethod, []string, error) {
	switch c.AuthType {
	case sshauth.KindPassword:
		if err := sshauth.ValidatePassword(c.Secret); err != nil {
			return nil, nil, err
		}
		// Some servers disable plain password auth and only accept PAM-style
		// keyboard-interactive. Offering both avoids a permanent
		// "unable to authenticate" on those hosts.
		interactive := ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range questions {
				answers[i] = c.Secret
			}
			return answers, nil
		})
		return []ssh.AuthMethod{ssh.Password(c.Secret), interactive}, []string{"password", "keyboard-interactive"}, nil
	case sshauth.KindPrivateKey:
		signer, err := sshauth.ParsePrivateKey(c.Secret, c.Passphrase)
		if err != nil {
			return nil, nil, err
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, []string{"publickey"}, nil
	default:
		return nil, nil, errors.New("unsupported credential type: " + c.AuthType)
	}
}

// Stage is one observable step of a connection attempt. The UI renders these in
// order so an operator can see exactly which link in the chain failed.
type Stage struct {
	Stage  string `json:"stage"`
	Label  string `json:"label"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"`
}

type Diagnostic struct {
	AssetID        string  `json:"asset_id"`
	AssetName      string  `json:"asset_name"`
	Username       string  `json:"username"`
	AuthType       string  `json:"auth_type"`
	Target         string  `json:"target,omitempty"`
	KeyFingerprint string  `json:"key_fingerprint,omitempty"`
	AuthorizedKey  string  `json:"authorized_key,omitempty"`
	Stages         []Stage `json:"stages"`
	OK             bool    `json:"ok"`
	Summary        string  `json:"summary"`
}

func (d *Diagnostic) add(stage Stage) {
	d.Stages = append(d.Stages, stage)
	if !stage.OK {
		d.OK = false
		if d.Summary == "" {
			d.Summary = stage.Label + "失败：" + stage.Detail
		}
	}
}

// Diagnose walks every step of an SSH connection and records the reason for each
// failure. It never returns a transport error for an expected failure, because
// the caller needs the full report.
func (m *Manager) Diagnose(ctx context.Context, assetID string) (Diagnostic, error) {
	a, c, err := m.store.CredentialForAsset(ctx, assetID)
	if err != nil {
		return Diagnostic{}, err
	}
	d := Diagnostic{AssetID: a.ID, AssetName: a.Name, Username: c.Username, AuthType: c.AuthType, OK: true}
	d.add(Stage{Stage: "credential", Label: "读取加密凭据", OK: true, Detail: "已从数据库解密 " + c.AuthType + " 凭据，用户 " + c.Username})

	var methods []ssh.AuthMethod
	var methodNames []string
	switch c.AuthType {
	case sshauth.KindPrivateKey:
		signer, perr := sshauth.ParsePrivateKey(c.Secret, c.Passphrase)
		if perr != nil {
			d.add(Stage{Stage: "credential_parse", Label: "解析私钥", OK: false, Detail: perr.Error(), Hint: "私钥需要 OpenSSH 格式；如果来自 PuTTY 请先转换，如果设置了口令请一并填写"})
			return d, nil
		}
		d.KeyFingerprint = sshauth.FingerprintSHA256(signer.PublicKey())
		d.AuthorizedKey = sshauth.AuthorizedKeyLine(signer)
		methods = []ssh.AuthMethod{ssh.PublicKeys(signer)}
		methodNames = []string{"publickey"}
		d.add(Stage{Stage: "credential_parse", Label: "解析私钥", OK: true, Detail: "私钥可用，公钥指纹 " + d.KeyFingerprint, Hint: "请确认目标机 ~/.ssh/authorized_keys 中存在这条公钥"})
	default:
		if perr := sshauth.ValidatePassword(c.Secret); perr != nil {
			d.add(Stage{Stage: "credential_parse", Label: "检查密码", OK: false, Detail: perr.Error(), Hint: "密码不能为空；如果是私钥请切换认证方式"})
			return d, nil
		}
		methods, methodNames, _ = authMethods(c)
		d.add(Stage{Stage: "credential_parse", Label: "检查密码", OK: true, Detail: "密码已就绪；将依次尝试 password 和 keyboard-interactive"})
	}

	if a.HostKey == "" {
		d.add(Stage{Stage: "host_key", Label: "主机指纹", OK: false, Detail: "尚未批准主机指纹", Hint: "先执行指纹扫描并通过带外渠道核对后再批准"})
		return d, nil
	}
	cb, err := hostKeyCallback(a.HostKey)
	if err != nil {
		d.add(Stage{Stage: "host_key", Label: "主机指纹", OK: false, Detail: "已保存的主机公钥无效", Hint: "请重新扫描并批准主机指纹"})
		return d, nil
	}

	target, err := m.ResolveTarget(ctx, a)
	if err != nil {
		d.add(Stage{Stage: "resolve", Label: "解析目标地址", OK: false, Detail: err.Error(), Hint: "确认地址为私网可路由地址，且位于 QIYUN_ALLOWED_CIDRS 允许网段内"})
		return d, nil
	}
	d.Target = target
	d.add(Stage{Stage: "resolve", Label: "解析目标地址", OK: true, Detail: target})

	dialer := net.Dialer{Timeout: 8 * time.Second}
	raw, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		d.add(Stage{Stage: "tcp", Label: "建立 TCP 连接", OK: false, Detail: err.Error(), Hint: tcpHint(err)})
		return d, nil
	}
	defer raw.Close()
	d.add(Stage{Stage: "tcp", Label: "建立 TCP 连接", OK: true, Detail: "已连接 " + target})

	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	stopHandshake := context.AfterFunc(ctx, func() { _ = raw.Close() })
	cfg := &ssh.ClientConfig{User: c.Username, Auth: methods, HostKeyCallback: cb, Timeout: 10 * time.Second}
	cc, ch, req, err := ssh.NewClientConn(raw, target, cfg)
	stopHandshake()
	_ = raw.SetDeadline(time.Time{})
	if err != nil {
		code, detail, hint := classifyDialError(err)
		stage, label := "authenticate", "SSH 握手与认证"
		if code == "host_key" {
			stage, label = "host_key_verify", "核对主机指纹"
		}
		d.add(Stage{Stage: stage, Label: label, OK: false, Detail: detail, Hint: hint})
		slog.Warn("ssh diagnose failed", "asset_id", a.ID, "stage", code, "target", target, "user", c.Username, "auth_type", c.AuthType, "key_fingerprint", d.KeyFingerprint, "detail", detail)
		return d, nil
	}
	client := ssh.NewClient(cc, ch, req)
	defer client.Close()
	d.add(Stage{Stage: "authenticate", Label: "SSH 握手与认证", OK: true, Detail: "认证通过，协商方法 " + strings.Join(methodNames, " / ")})

	session, err := client.NewSession()
	if err != nil {
		d.add(Stage{Stage: "session", Label: "打开会话通道", OK: false, Detail: err.Error(), Hint: "账号可能被限制为不支持 exec/shell，请检查 sshd 的 ForceCommand 或权限"})
		return d, nil
	}
	err = session.Run("true")
	session.Close()
	if err != nil {
		d.add(Stage{Stage: "exec", Label: "执行探测命令", OK: false, Detail: err.Error(), Hint: "账号可能没有 shell，或被 ForceCommand 限制为单一命令"})
		return d, nil
	}
	d.add(Stage{Stage: "exec", Label: "执行探测命令", OK: true, Detail: "远端成功执行 true，可直接建立交互终端"})
	slog.Info("ssh diagnose ok", "asset_id", a.ID, "target", target, "user", c.Username, "auth_type", c.AuthType, "key_fingerprint", d.KeyFingerprint)
	return d, nil
}

// classifyDialError turns a terse crypto/ssh message into an actionable reason.
func classifyDialError(err error) (code, detail, hint string) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "HOST_KEY_MISMATCH"):
		return "host_key", msg, "目标机主机公钥已变化。请确认不是中间人或重装系统，核对后重新扫描并批准指纹"
	case strings.Contains(msg, "unable to authenticate"):
		return "authenticate", "服务端拒绝了提供的凭据（" + msg + "）", "核对用户名、密码或私钥，并确认目标机 sshd 允许该认证方式；私钥需存在于对应用户的 authorized_keys"
	case strings.Contains(msg, "no supported methods remain"):
		return "authenticate", "服务端没有接受任何已提供的认证方式", "服务端可能关闭了密码登录，或只允许公钥登录；请在 sshd_config 中确认 PasswordAuthentication / PubkeyAuthentication"
	case strings.Contains(msg, "handshake failed"):
		return "handshake", msg, "目标端口可能不是 SSH，或 TLS/协议不匹配；确认端口号与 sshd 服务"
	default:
		return "handshake", msg, "请检查目标机 sshd 日志 /var/log/auth.log 获取服务端拒绝原因"
	}
}

func tcpHint(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "no route to host"):
		return "没有到达目标的路由。若服务运行在 WSL 中，WSL2 默认 NAT 无法访问局域网地址；请在 Windows 本机运行 Go 服务，或在 .wslconfig 启用 networkingMode=mirrored"
	case strings.Contains(msg, "connection refused"):
		return "目标端口没有监听；确认 sshd 已启动且端口正确（PVE 默认 22，也可自定义如 50022）"
	case strings.Contains(msg, "i/o timeout"), strings.Contains(msg, "deadline exceeded"):
		return "网络不可达或被防火墙拦截；确认本机到目标网段的路由与安全组，WSL 环境请检查是否为 NAT 模式"
	default:
		return "确认地址、端口和网络连通性；可用 Windows 的 Test-NetConnection 先验证目标端口"
	}
}

func (m *Manager) Dial(ctx context.Context, assetID string) (*ssh.Client, store.Asset, string, error) {
	a, c, err := m.store.CredentialForAsset(ctx, assetID)
	if err != nil {
		return nil, a, "", err
	}
	if a.HostKey == "" {
		return nil, a, "", errors.New("HOST_KEY_UNKNOWN: approve the SSH host key before connecting")
	}
	target, err := m.ResolveTarget(ctx, a)
	if err != nil {
		return nil, a, "", err
	}
	cb, err := hostKeyCallback(a.HostKey)
	if err != nil {
		return nil, a, target, err
	}
	methods, methodNames, err := authMethods(c)
	if err != nil {
		return nil, a, target, err
	}
	started := time.Now()
	d := net.Dialer{Timeout: 10 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		slog.Warn("ssh dial failed", "asset_id", a.ID, "target", target, "stage", "tcp", "error", err)
		return nil, a, target, fmt.Errorf("dial SSH: %w", err)
	}
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	stopHandshake := context.AfterFunc(ctx, func() { _ = raw.Close() })
	cfg := &ssh.ClientConfig{User: c.Username, Auth: methods, HostKeyCallback: cb, Timeout: 10 * time.Second}
	cc, ch, req, err := ssh.NewClientConn(raw, target, cfg)
	stopHandshake()
	_ = raw.SetDeadline(time.Time{})
	if err != nil {
		raw.Close()
		code, detail, hint := classifyDialError(err)
		slog.Warn("ssh handshake failed", "asset_id", a.ID, "target", target, "user", c.Username, "auth_type", c.AuthType, "stage", code, "detail", detail, "hint", hint)
		return nil, a, target, fmt.Errorf("SSH handshake: %w", err)
	}
	slog.Info("ssh connected", "asset_id", a.ID, "target", target, "user", c.Username, "auth_type", c.AuthType, "methods", strings.Join(methodNames, ","), "duration_ms", time.Since(started).Milliseconds())
	return ssh.NewClient(cc, ch, req), a, target, nil
}

func (m *Manager) Test(ctx context.Context, assetID string) (Diagnostic, error) {
	d, err := m.Diagnose(ctx, assetID)
	if err != nil {
		return d, err
	}
	if !d.OK {
		return d, errors.New(d.Summary)
	}
	return d, nil
}

func (m *Manager) CreateTicket(assetID string) (*Ticket, error) {
	return m.CreateTicketWithSize(assetID, 0, 0)
}

// CreateTicketWithSize validates the requested PTY size (clamped to sane bounds)
// and prepares a single-use WebSocket ticket bound to the asset.
func (m *Manager) CreateTicketWithSize(assetID string, cols, rows int) (*Ticket, error) {
	asset, err := m.store.GetAsset(context.Background(), assetID)
	if err != nil {
		return nil, err
	}
	if asset.HostKey == "" {
		return nil, errors.New("HOST_KEY_UNKNOWN: approve the SSH host key before creating a terminal session")
	}
	if cols < 20 || cols > 500 {
		cols = 120
	}
	if rows < 5 || rows > 300 {
		rows = 32
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	t := &Ticket{SessionID: uuid.NewString(), AssetID: assetID, Value: base64.RawURLEncoding.EncodeToString(raw), Expires: time.Now().Add(60 * time.Second), Cols: cols, Rows: rows}
	m.mu.Lock()
	m.tickets[t.SessionID] = t
	m.mu.Unlock()
	return t, nil
}
func (m *Manager) consume(id, value string) (*Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tickets[id]
	if t == nil || t.Used || time.Now().After(t.Expires) || value == "" || t.Value != value {
		if t != nil && time.Now().After(t.Expires) {
			delete(m.tickets, id)
		}
		return nil, errors.New("invalid or expired WebSocket ticket")
	}
	t.Used = true
	delete(m.tickets, id)
	return t, nil
}

func (m *Manager) ServeWS(w http.ResponseWriter, r *http.Request) {
	// Every branch below logs: a silent failure here used to leave operators with
	// an empty terminal and no trace of why.
	sessionID := r.PathValue("id")
	slog.Info("terminal websocket requested", "session_id", sessionID, "origin", r.Header.Get("Origin"), "host", r.Host, "remote", r.RemoteAddr)
	t, err := m.consume(sessionID, r.URL.Query().Get("ticket"))
	if err != nil {
		slog.Warn("terminal websocket rejected", "session_id", sessionID, "reason", "invalid or expired ticket", "detail", err.Error())
		http.Error(w, "invalid or expired WebSocket ticket", http.StatusUnauthorized)
		return
	}
	opts := &websocket.AcceptOptions{OriginPatterns: m.origins}
	conn, err := websocket.Accept(w, r, opts)
	if err != nil {
		slog.Warn("terminal websocket handshake failed", "session_id", sessionID, "asset_id", t.AssetID, "origin", r.Header.Get("Origin"), "host", r.Host, "error", err)
		m.store.Audit(context.Background(), "terminal.connect", "asset", t.AssetID, "failed", map[string]any{"stage": "websocket_handshake", "error": err.Error()})
		return
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Hour)
	defer cancel()
	client, asset, target, err := m.Dial(ctx, t.AssetID)
	if err != nil {
		code := "SSH_CONNECT_FAILED"
		if strings.Contains(err.Error(), "HOST_KEY_MISMATCH") {
			code = "HOST_KEY_MISMATCH"
		} else if strings.Contains(err.Error(), "HOST_KEY_UNKNOWN") {
			code = "HOST_KEY_UNKNOWN"
		}
		slog.Warn("terminal ssh connect failed", "session_id", sessionID, "asset_id", t.AssetID, "code", code, "error", err)
		_ = conn.Write(ctx, websocket.MessageText, mustJSON(map[string]any{"type": "error", "code": code, "message": err.Error()}))
		m.store.Audit(context.Background(), "terminal.connect", "asset", t.AssetID, "failed", map[string]any{"code": code, "error": err.Error()})
		return
	}
	defer client.Close()
	conn.SetReadLimit(65536)
	_ = m.store.StartSession(context.Background(), t.SessionID, t.AssetID, target, asset.HostKeyFingerprint)
	session, err := client.NewSession()
	if err != nil {
		slog.Warn("terminal session create failed", "session_id", sessionID, "asset_id", t.AssetID, "error", err)
		m.failSession(t.SessionID, err)
		return
	}
	defer session.Close()
	stdin, err := session.StdinPipe()
	if err != nil {
		slog.Warn("terminal stdin pipe failed", "session_id", sessionID, "asset_id", t.AssetID, "error", err)
		m.failSession(t.SessionID, err)
		return
	}
	outR, outW := io.Pipe()
	session.Stdout = outW
	session.Stderr = outW
	if err = session.RequestPty("xterm-256color", t.Rows, t.Cols, ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}); err != nil {
		slog.Warn("terminal pty request failed", "session_id", sessionID, "asset_id", t.AssetID, "error", err)
		m.failSession(t.SessionID, err)
		return
	}
	// Start the interactive shell immediately. The tool probe used to run before
	// this point and could block the terminal for up to 75 seconds.
	err = session.Start("sh -c " + shellQuote(toolPath+"\nexec \"${SHELL:-/bin/sh}\" -i"))
	if err != nil {
		// 部分账号禁止 exec 但允许 shell，不能因工具 PATH 包装失去终端。
		err = session.Shell()
	}
	if err != nil {
		slog.Warn("terminal shell failed", "session_id", sessionID, "asset_id", t.AssetID, "error", err)
		m.failSession(t.SessionID, err)
		return
	}
	slog.Info("terminal session open", "session_id", sessionID, "asset_id", t.AssetID, "target", target, "user", asset.Username)
	_ = m.store.UpdateSession(context.Background(), t.SessionID, "connected", "", 0, 0)
	_ = conn.Write(ctx, websocket.MessageText, mustJSON(map[string]any{"type": "ready", "session_id": t.SessionID, "host_key_fingerprint": asset.HostKeyFingerprint, "max_frame_bytes": 65536, "absolute_timeout_seconds": 28800}))
	var bytesIn, bytesOut atomic.Int64
	writeMu := sync.Mutex{}
	done := make(chan error, 3)
	go func() {
		// Independent SSH session: the interactive shell stays responsive even
		// when the remote needs to download and install the transfer tools.
		prepareCtx, prepareCancel := context.WithTimeout(ctx, 75*time.Second)
		defer prepareCancel()
		err := prepareTransferTools(prepareCtx, client)
		message := "trz/tsz 已就绪（已有命令优先）；trz 上传，tsz 文件名 下载。"
		if err != nil {
			message = "文件传输工具准备失败：" + err.Error() + "。终端仍可使用；请检查原因后关闭此终端标签，再点击资产打开终端重试。"
		}
		writeMu.Lock()
		_ = conn.Write(ctx, websocket.MessageText, mustJSON(map[string]any{"type": "transfer_tools", "ok": err == nil, "message": message}))
		writeMu.Unlock()
		slog.Info("transfer tools prepared", "session_id", sessionID, "asset_id", t.AssetID, "ok", err == nil)
	}()
	go func() {
		err := session.Wait()
		_ = outW.Close()
		done <- err
	}()
	go func() {
		buf := make([]byte, 32768)
		for {
			n, e := outR.Read(buf)
			if n > 0 {
				bytesOut.Add(int64(n))
				writeMu.Lock()
				we := conn.Write(ctx, websocket.MessageBinary, append([]byte(nil), buf[:n]...))
				writeMu.Unlock()
				if we != nil {
					done <- we
					return
				}
			}
			if e != nil {
				done <- e
				return
			}
		}
	}()
	go func() {
		for {
			kind, data, e := conn.Read(ctx)
			if e != nil {
				done <- e
				return
			}
			if len(data) > 65536 {
				done <- errors.New("terminal frame exceeds 64 KiB")
				return
			}
			if kind == websocket.MessageBinary {
				bytesIn.Add(int64(len(data)))
				if _, e = stdin.Write(data); e != nil {
					done <- e
					return
				}
				continue
			}
			var msg struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
				Name string `json:"name"`
			}
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			switch msg.Type {
			case "resize":
				if msg.Cols >= 20 && msg.Cols <= 500 && msg.Rows >= 5 && msg.Rows <= 300 {
					_ = session.WindowChange(msg.Rows, msg.Cols)
				}
			case "signal":
				if msg.Name == "INT" {
					_ = session.Signal(ssh.SIGINT)
				} else if msg.Name == "TERM" {
					_ = session.Signal(ssh.SIGTERM)
				}
			case "ping":
				writeMu.Lock()
				_ = conn.Write(ctx, websocket.MessageText, mustJSON(map[string]any{"type": "pong"}))
				writeMu.Unlock()
			}
		}
	}()
	err = <-done
	_ = stdin.Close()
	_ = outW.Close()
	_ = session.Close()
	_ = client.Close()
	reason := "client disconnected"
	if err != nil && !errors.Is(err, io.EOF) {
		reason = err.Error()
	}
	_ = m.store.UpdateSession(context.Background(), t.SessionID, "closed", reason, bytesIn.Load(), bytesOut.Load())
	slog.Info("terminal session closed", "session_id", sessionID, "asset_id", t.AssetID, "bytes_in", bytesIn.Load(), "bytes_out", bytesOut.Load(), "reason", reason)
	m.store.Audit(context.Background(), "terminal.close", "asset", t.AssetID, "success", map[string]any{"session_id": t.SessionID, "bytes_in": bytesIn.Load(), "bytes_out": bytesOut.Load()})
	_ = conn.Close(websocket.StatusNormalClosure, "session closed")
}
func (m *Manager) failSession(id string, err error) {
	_ = m.store.UpdateSession(context.Background(), id, "failed", err.Error(), 0, 0)
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
