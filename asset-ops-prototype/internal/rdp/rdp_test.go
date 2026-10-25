package rdp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"qiyun-asset-ops/internal/store"
)

type fakeSource struct {
	asset  store.Asset
	cred   store.Credential
	err    error
	calls  int
	lastID string
}

func (f *fakeSource) CredentialForAsset(_ context.Context, id string) (store.Asset, store.Credential, error) {
	f.calls++
	f.lastID = id
	return f.asset, f.cred, f.err
}

type auditEvent struct {
	action, kind, id, result string
	metadata                 map[string]any
}

type fakeAudit struct {
	mu     sync.Mutex
	events []auditEvent
}

func (f *fakeAudit) Audit(_ context.Context, action, kind, id, result string, metadata any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	meta, _ := metadata.(map[string]any)
	f.events = append(f.events, auditEvent{action: action, kind: kind, id: id, result: result, metadata: meta})
}

func (f *fakeAudit) last() (auditEvent, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.events) == 0 {
		return auditEvent{}, false
	}
	return f.events[len(f.events)-1], true
}

type fakeExec struct {
	fn    func(Call) (Output, error)
	calls []Call
}

func (f *fakeExec) Run(_ context.Context, call Call) (Output, error) {
	f.calls = append(f.calls, call)
	if f.fn != nil {
		return f.fn(call)
	}
	return Output{}, nil
}

func windowsAsset() store.Asset {
	return store.Asset{
		ID: "asset-win-1", AssetCode: "WIN-RDS-1", Name: "win-rds-01",
		Address: "10.20.30.33", SSHPort: 22, RDPPort: 3390,
		OSFamily: "windows", OSName: "Windows Server 2022", AssetType: "windows_host",
	}
}

func passwordCredential() store.Credential {
	return store.Credential{ID: "cred-1", Username: `CORP\ops`, AuthType: "password", Secret: "s3cret-value"}
}

func newManager(t *testing.T, platform string, exec Executor) (*Manager, *fakeSource, *fakeAudit) {
	t.Helper()
	source := &fakeSource{asset: windowsAsset(), cred: passwordCredential()}
	audit := &fakeAudit{}
	m, err := New(source, audit, Options{DataDir: t.TempDir(), Platform: platform, Executor: exec})
	if err != nil {
		t.Fatal(err)
	}
	return m, source, audit
}

func TestNewExtractsLauncherScriptsOnWindows(t *testing.T) {
	dir := t.TempDir()
	m, err := New(&fakeSource{asset: windowsAsset(), cred: passwordCredential()}, &fakeAudit{}, Options{
		DataDir: dir, Platform: "windows", Executor: &fakeExec{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"launch-rdp.ps1", "setup-rdp-signing.ps1"} {
		info, err := os.Stat(m.launchPath)
		if name == "setup-rdp-signing.ps1" {
			info, err = os.Stat(m.signingPath)
		}
		if err != nil || info.Size() < 1000 {
			t.Fatalf("%s not extracted: %v", name, err)
		}
	}
	if filepath.Base(m.launchPath) != "launch-rdp.ps1" {
		t.Fatalf("unexpected launcher path %s", m.launchPath)
	}
}

// The launcher must receive the asset's stored credential, never a value the
// browser could have supplied for the address or account.
func TestLaunchBuildsProfileFromAssetCredential(t *testing.T) {
	exec := &fakeExec{fn: func(Call) (Output, error) {
		return Output{Stdout: `{"ok":true,"pid":4242,"target":"10.20.30.33:3390"}`}, nil
	}}
	m, source, audit := newManager(t, "windows", exec)
	result, err := m.Launch(context.Background(), "asset-win-1", LaunchInput{
		RedirectClipboard: true, RedirectDrives: true, DriveMode: "custom",
		DriveList: "D:,E:\\", Fullscreen: true, MultiMonitor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.PID != 4242 || result.Target != "10.20.30.33:3390" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if source.lastID != "asset-win-1" {
		t.Fatalf("asset id not forwarded: %q", source.lastID)
	}
	if len(exec.calls) != 1 {
		t.Fatalf("expected one launcher call, got %d", len(exec.calls))
	}
	call := exec.calls[0]
	if !strings.HasSuffix(call.Script, "launch-rdp.ps1") || !call.FirstJSON {
		t.Fatalf("launcher call not shaped as expected: %#v", call)
	}
	var profile Profile
	if err := json.Unmarshal([]byte(call.Stdin), &profile); err != nil {
		t.Fatalf("stdin is not a profile: %v (%s)", err, call.Stdin)
	}
	if profile.Address != "10.20.30.33" || profile.Port != 3390 || profile.Username != `CORP\ops` || profile.Password != "s3cret-value" {
		t.Fatalf("profile lost server-side identity: %#v", profile)
	}
	if !profile.RedirectClipboard || !profile.RedirectDrives || profile.DriveMode != "custom" || profile.DriveList != "D:,E:\\" || !profile.Fullscreen || !profile.MultiMonitor {
		t.Fatalf("resource options not forwarded: %#v", profile)
	}
	event, ok := audit.last()
	if !ok || event.action != "asset.rdp.launch" || event.result != "success" {
		t.Fatalf("missing success audit: %#v", event)
	}
	if _, leaked := event.metadata["password"]; leaked {
		t.Fatal("audit metadata must never contain the password")
	}
	if strings.Contains(strings.ToLower(strings.Join(keys(event.metadata), ",")), "password") {
		t.Fatalf("audit metadata mentions a password field: %#v", event.metadata)
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestLaunchInteractiveModeSendsNoPassword(t *testing.T) {
	exec := &fakeExec{fn: func(Call) (Output, error) {
		return Output{Stdout: `{"ok":true,"pid":7,"target":"10.20.30.33:3389"}`}, nil
	}}
	m, _, _ := newManager(t, "windows", exec)
	if _, err := m.Launch(context.Background(), "asset-win-1", LaunchInput{PromptCredentials: true}); err != nil {
		t.Fatal(err)
	}
	var profile Profile
	if err := json.Unmarshal([]byte(exec.calls[0].Stdin), &profile); err != nil {
		t.Fatal(err)
	}
	if profile.Username != "" || profile.Password != "" {
		t.Fatalf("interactive mode must not send stored credentials: %#v", profile)
	}
}

func TestLaunchRejectsNonWindowsAsset(t *testing.T) {
	source := &fakeSource{asset: windowsAsset(), cred: passwordCredential()}
	source.asset.OSFamily = "linux"
	source.asset.AssetType = "linux_host"
	source.asset.OSName = "Ubuntu 24.04"
	m, err := New(source, &fakeAudit{}, Options{DataDir: t.TempDir(), Platform: "windows", Executor: &fakeExec{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Launch(context.Background(), "asset-win-1", LaunchInput{})
	assertCode(t, err, "RDP_ASSET_NOT_WINDOWS", 400)
}

func TestLaunchRequiresPasswordCredential(t *testing.T) {
	source := &fakeSource{asset: windowsAsset(), cred: store.Credential{Username: "ops", AuthType: "private_key", Secret: "key"}}
	m, err := New(source, &fakeAudit{}, Options{DataDir: t.TempDir(), Platform: "windows", Executor: &fakeExec{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Launch(context.Background(), "asset-win-1", LaunchInput{})
	assertCode(t, err, "RDP_PASSWORD_REQUIRED", 409)
}

func TestLaunchMapsSigningRequired(t *testing.T) {
	exec := &fakeExec{fn: func(Call) (Output, error) {
		return Output{Stdout: `{"ok":false,"code":"signing_setup_required","error":"One-click RDP signing setup is required"}`, ExitCode: 2}, nil
	}}
	m, _, audit := newManager(t, "windows", exec)
	_, err := m.Launch(context.Background(), "asset-win-1", LaunchInput{})
	assertCode(t, err, "RDP_SIGNING_REQUIRED", 409)
	event, _ := audit.last()
	if event.result != "failed" {
		t.Fatalf("signing failure not audited: %#v", event)
	}
}

func TestLaunchMapsLauncherFailure(t *testing.T) {
	exec := &fakeExec{fn: func(Call) (Output, error) {
		return Output{Stdout: `{"ok":false,"error":"rdpsign failed: denied"}`}, nil
	}}
	m, _, _ := newManager(t, "windows", exec)
	_, err := m.Launch(context.Background(), "asset-win-1", LaunchInput{})
	assertCode(t, err, "RDP_LAUNCH_FAILED", 502)
	if !strings.Contains(err.Error(), "rdpsign failed") {
		t.Fatalf("launcher message lost: %v", err)
	}
}

func TestLaunchTimesOutWithoutStatusLine(t *testing.T) {
	exec := &fakeExec{fn: func(Call) (Output, error) { return Output{}, context.DeadlineExceeded }}
	m, _, _ := newManager(t, "windows", exec)
	m.launchTimeout = 10 * time.Millisecond
	_, err := m.Launch(context.Background(), "asset-win-1", LaunchInput{})
	assertCode(t, err, "RDP_LAUNCH_TIMEOUT", 504)
}

func TestCapabilitiesOnNonWindows(t *testing.T) {
	exec := &fakeExec{}
	m, _, _ := newManager(t, "linux", exec)
	cap := m.Capabilities(context.Background())
	if cap.Available || cap.Platform != "linux" || cap.Error == "" {
		t.Fatalf("non-Windows capability must be explicit: %#v", cap)
	}
	if len(exec.calls) != 0 {
		t.Fatal("capability check must not spawn a launcher on non-Windows")
	}
}

func TestCapabilitiesAndCaching(t *testing.T) {
	exec := &fakeExec{fn: func(call Call) (Output, error) {
		switch {
		case len(call.Args) > 0 && call.Args[0] == "-SelfTest":
			return Output{Stdout: `{"ok":true,"credentialApi":true,"mstsc":true,"rdpsign":true}`}, nil
		default:
			return Output{Stdout: `{"ok":true,"configured":true,"signingHash":"sha256","publisher":"Pulse RDP Launcher"}`}, nil
		}
	}}
	m, _, _ := newManager(t, "windows", exec)
	cap := m.Capabilities(context.Background())
	if !cap.Available || !cap.MSTSC || !cap.Signing.Configured {
		t.Fatalf("unexpected capability: %#v", cap)
	}
	calls := len(exec.calls)
	m.Capabilities(context.Background())
	if len(exec.calls) != calls {
		t.Fatal("capabilities should be cached for 30 seconds")
	}
}

func TestSetupSigningInvalidatesCapabilityCache(t *testing.T) {
	exec := &fakeExec{fn: func(call Call) (Output, error) {
		if len(call.Args) > 0 && call.Args[0] == "-SelfTest" {
			return Output{Stdout: `{"ok":true,"credentialApi":true,"mstsc":true,"rdpsign":true}`}, nil
		}
		return Output{Stdout: `{"ok":true,"configured":true,"signingHash":"sha256"}`}, nil
	}}
	m, _, audit := newManager(t, "windows", exec)
	if !m.Capabilities(context.Background()).Signing.Configured {
		t.Fatal("expected configured signing")
	}
	before := len(exec.calls)
	status, err := m.SetupSigning(context.Background())
	if err != nil || !status.Configured {
		t.Fatalf("setup failed: %#v %v", status, err)
	}
	if len(exec.calls) != before+1 {
		t.Fatalf("unexpected setup calls: %d -> %d", before, len(exec.calls))
	}
	m.Capabilities(context.Background())
	if len(exec.calls) < before+3 {
		t.Fatal("setup must invalidate the capability cache")
	}
	event, _ := audit.last()
	if event.action != "rdp.signing.setup" || event.result != "success" {
		t.Fatalf("setup not audited: %#v", event)
	}
}

func TestSigningSetupFailures(t *testing.T) {
	cases := []struct {
		name, stdout, stderr, code string
		exitCode, status           int
		err                        error
	}{
		{"private key access", "", "CertEnroll::_CreateRequest: Access denied. 0x80090010 (-2146893808 NTE_PERM) at private-path:147", "RDP_SIGNING_ACCESS_DENIED", 1, 403, nil},
		{"registry access", "", "Access is denied 0x80070005", "RDP_SIGNING_ACCESS_DENIED", 1, 403, nil},
		{"timeout", "", "", "RDP_SIGNING_TIMEOUT", -1, 504, context.DeadlineExceeded},
		{"process missing", "", "", "RDP_SIGNING_SETUP_FAILED", -1, 502, os.ErrNotExist},
		{"invalid result", "broken output", "", "RDP_SIGNING_INVALID_RESULT", 0, 502, nil},
		{"partial installation", `{"ok":true,"configured":false}`, "", "RDP_SIGNING_INCOMPLETE", 0, 409, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			executor := &fakeExec{fn: func(Call) (Output, error) {
				return Output{Stdout: tc.stdout, Stderr: tc.stderr, ExitCode: tc.exitCode}, tc.err
			}}
			m, _, audit := newManager(t, "windows", executor)
			m.caps = &Capability{Available: true, Signing: SigningStatus{OK: true, Configured: true}}
			_, err := m.SetupSigning(context.Background())
			assertCode(t, err, tc.code, tc.status)
			if strings.Contains(err.Error(), "private-path") || strings.Contains(err.Error(), "CertEnroll") {
				t.Fatal("public error contains raw PowerShell diagnostics")
			}
			if m.caps != nil {
				t.Fatal("failed installation retained stale capability cache")
			}
			event, ok := audit.last()
			if !ok || event.result != "failed" || event.metadata["code"] != tc.code {
				t.Fatalf("missing failure audit: %#v", event)
			}
		})
	}
}

func TestValidateProfiles(t *testing.T) {
	valid := Profile{Address: "10.0.0.5", Port: 3389, Username: `CORP\ops`, Password: "x"}
	if err := Validate(valid); err != nil {
		t.Fatalf("valid profile rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*Profile)
		code   string
	}{
		{"empty address", func(p *Profile) { p.Address = "" }, "RDP_INVALID_TARGET"},
		{"space in address", func(p *Profile) { p.Address = "10.0.0.5 evil" }, "RDP_INVALID_TARGET"},
		{"zero port", func(p *Profile) { p.Port = 0 }, "RDP_INVALID_PORT"},
		{"huge port", func(p *Profile) { p.Port = 70000 }, "RDP_INVALID_PORT"},
		{"newline username", func(p *Profile) { p.Username = "a\r\nb" }, "RDP_INVALID_USERNAME"},
		{"password without username", func(p *Profile) { p.Username = "" }, "RDP_INVALID_USERNAME"},
		{"oversize password", func(p *Profile) { p.Password = strings.Repeat("x", 2049) }, "RDP_PASSWORD_TOO_LONG"},
		{"empty drive list", func(p *Profile) {
			p.RedirectDrives = true
			p.DriveMode = "custom"
			p.DriveList = " "
		}, "RDP_INVALID_DRIVES"},
		{"bad drive", func(p *Profile) {
			p.RedirectDrives = true
			p.DriveMode = "custom"
			p.DriveList = "D:,HOME"
		}, "RDP_INVALID_DRIVES"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile := valid
			tc.mutate(&profile)
			assertCode(t, Validate(profile), tc.code, 400)
		})
	}
}

func TestDecodeJSONTolerantOfBOMAndNoise(t *testing.T) {
	var value struct {
		OK bool `json:"ok"`
	}
	if !decodeFirstJSON("\ufeff\r\nnoise\r\n{\"ok\":true}\r\n", &value) {
		t.Fatal("BOM and noise should be skipped")
	}
	if !value.OK {
		t.Fatal("value not decoded")
	}
	if decodeFirstJSON("plain text", &value) {
		t.Fatal("non-JSON must not decode")
	}
}

func assertCode(t *testing.T, err error, code string, status int) {
	t.Helper()
	var rdpErr *RDPError
	if !errors.As(err, &rdpErr) {
		t.Fatalf("expected RDPError %s, got %v", code, err)
	}
	if rdpErr.Code != code || rdpErr.Status != status {
		t.Fatalf("expected %s/%d, got %s/%d (%s)", code, status, rdpErr.Code, rdpErr.Status, rdpErr.Message)
	}
}

// The launcher prints its status line, then waits ~30 seconds to delete the
// temporary credential and .rdp file. Returning the HTTP response must not kill
// that child: an earlier version bound it to the request context and left
// TERMSRV/<host> credentials and pulse-rdp-*.rdp files behind. This test runs a
// real PowerShell script that prints JSON, then writes a marker after a delay.
func TestProcessExecutorKeepsCleanupAlive(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell launcher is Windows-only")
	}
	// Keep the marker inside the workspace: sandboxed environments allow writes
	// there but not in the system temp directory.
	dir, err := os.MkdirTemp(".", "rdp-lifecycle-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	marker := filepath.Join(dir, "cleanup-marker.txt")
	script := filepath.Join(dir, "slow-launcher.ps1")
	body := `[Console]::Out.WriteLine('{"ok":true,"pid":1}')
Start-Sleep -Milliseconds 700
Set-Content -LiteralPath '` + strings.ReplaceAll(marker, "'", "''") + `' -Value done
`
	if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := &processExecutor{powershell: powerShellPath("")}
	ctx, cancel := context.WithCancel(context.Background())
	out, err := executor.Run(ctx, Call{Script: script, FirstJSON: true})
	if err != nil {
		t.Fatalf("launcher did not return its status line: %v", err)
	}
	if !strings.Contains(out.Stdout, `"ok":true`) {
		t.Fatalf("unexpected status line: %q", out.Stdout)
	}
	// This used to kill the still-running cleanup phase.
	cancel()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("launcher was killed with the request context; delayed cleanup never completed")
}
