package transfer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"qiyun-asset-ops/internal/store"
)

// --- in-memory remote used instead of a real SSH/SFTP server ---

type memFS struct {
	mu         sync.Mutex
	files      map[string][]byte
	dirs       map[string]bool
	failWrites int // number of upcoming WriteAt calls that should fail
	// Mirrors the server extension list. Testing with it false covers the
	// non-OpenSSH servers where an overwrite cannot be atomic.
	noPosixRename bool
}

func newMemFS() *memFS {
	return &memFS{files: map[string][]byte{}, dirs: map[string]bool{"/": true, "/tmp": true}}
}

type memFileInfo struct {
	name  string
	size  int64
	isDir bool
}

func (i memFileInfo) Name() string { return i.name }
func (i memFileInfo) Size() int64  { return i.size }
func (i memFileInfo) Mode() os.FileMode {
	if i.isDir {
		return os.ModeDir | 0o755
	}
	return 0o644
}
func (i memFileInfo) ModTime() time.Time { return time.Unix(0, 0) }
func (i memFileInfo) IsDir() bool        { return i.isDir }
func (i memFileInfo) Sys() any           { return nil }

func (m *memFS) Stat(name string) (os.FileInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dirs[name] {
		return memFileInfo{name: name, isDir: true}, nil
	}
	if data, ok := m.files[name]; ok {
		return memFileInfo{name: name, size: int64(len(data))}, nil
	}
	return nil, os.ErrNotExist
}

func (m *memFS) MkdirAll(dir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dirs[dir] = true
	return nil
}

func (m *memFS) OpenFile(name string, flag int) (RemoteFile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[name]; !ok {
		if flag&os.O_CREATE == 0 {
			return nil, os.ErrNotExist
		}
		m.files[name] = []byte{}
	}
	if flag&os.O_TRUNC != 0 {
		m.files[name] = []byte{}
	}
	return &memFile{fs: m, name: name}, nil
}

func (m *memFS) Open(name string) (RemoteFile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[name]; !ok {
		return nil, os.ErrNotExist
	}
	return &memFile{fs: m, name: name}, nil
}

func (m *memFS) Create(name string) (RemoteFile, error) {
	m.mu.Lock()
	m.files[name] = []byte{}
	m.mu.Unlock()
	return &memFile{fs: m, name: name}, nil
}

// Rename mirrors SFTP v3: it must refuse to replace an existing file. An
// earlier, more permissive double hid a real bug where "允许覆盖" only failed
// against a real server, at the very last step of the upload.
func (m *memFS) Rename(oldpath, newpath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[oldpath]
	if !ok {
		return os.ErrNotExist
	}
	if _, exists := m.files[newpath]; exists {
		return errors.New(`sftp: "Failure" (SSH_FX_FAILURE)`)
	}
	m.files[newpath] = data
	delete(m.files, oldpath)
	return nil
}

// PosixRename mirrors posix-rename@openssh.com: an atomic replace.
func (m *memFS) PosixRename(oldpath, newpath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[oldpath]
	if !ok {
		return os.ErrNotExist
	}
	m.files[newpath] = data
	delete(m.files, oldpath)
	return nil
}

func (m *memFS) SupportsPosixRename() bool { return !m.noPosixRename }

// exists reads the map under the lock so cleanup goroutines can be tested.
func (m *memFS) exists(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.files[name]
	return ok
}

func (m *memFS) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, name)
	return nil
}

func (m *memFS) Close() error { return nil }

type memFile struct {
	fs     *memFS
	name   string
	closed bool
}

func (f *memFile) ReadAt(p []byte, off int64) (int, error) {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	data := f.fs.files[f.name]
	if off >= int64(len(data)) {
		return 0, io.EOF
	}
	n := copy(p, data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *memFile) WriteAt(p []byte, off int64) (int, error) {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	if f.fs.failWrites > 0 {
		f.fs.failWrites--
		return 0, errors.New("connection lost")
	}
	data := f.fs.files[f.name]
	if need := off + int64(len(p)); need > int64(len(data)) {
		grown := make([]byte, need)
		copy(grown, data)
		data = grown
	}
	copy(data[off:], p)
	f.fs.files[f.name] = data
	return len(p), nil
}

func (f *memFile) Truncate(size int64) error {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	data := f.fs.files[f.name]
	if size < int64(len(data)) {
		f.fs.files[f.name] = data[:size]
	}
	return nil
}

func (f *memFile) Close() error { f.closed = true; return nil }

// --- fixtures ---

type fakeAssets struct{ items map[string]store.Asset }

func (f fakeAssets) GetAsset(_ context.Context, id string) (store.Asset, error) {
	a, ok := f.items[id]
	if !ok {
		return store.Asset{}, fmt.Errorf("asset %s not found", id)
	}
	return a, nil
}

type countingAudit struct {
	mu     sync.Mutex
	events []string
}

func (c *countingAudit) Audit(_ context.Context, action, _, _, result string, _ any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, action+":"+result)
}

func (c *countingAudit) count(action string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.events {
		if strings.HasPrefix(e, action+":") {
			n++
		}
	}
	return n
}

func newTestManager(t *testing.T, fs *memFS, dir string) (*Manager, *countingAudit) {
	t.Helper()
	assets := fakeAssets{items: map[string]store.Asset{
		"asset-1":      {ID: "asset-1", Name: "生产主机", HostKey: "ssh-ed25519 AAAA", HostKeyStatus: "verified"},
		"asset-locked": {ID: "asset-locked", Name: "未核验主机"},
	}}
	audit := &countingAudit{}
	m, err := New(assets, func(context.Context, string) (Remote, error) { return fs, nil }, audit,
		Options{DataDir: dir, MaxTasks: 2})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	return m, audit
}

func mustCreateUpload(t *testing.T, m *Manager, path string, size int64, overwrite bool) Task {
	t.Helper()
	task, err := m.Create(context.Background(), CreateRequest{
		AssetID: "asset-1", Direction: DirectionUpload, RemotePath: path, Size: size, Overwrite: overwrite,
	})
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	return task
}

// --- validation ---

func TestValidateRemotePath(t *testing.T) {
	good := []string{"/tmp/a.iso", "/home/u/data.bin", "/srv/x/y.tar.gz", "/a"}
	for _, p := range good {
		if err := ValidateRemotePath(p); err != nil {
			t.Errorf("expected %q to be valid, got %v", p, err)
		}
	}
	bad := []string{"", "tmp/a", "/tmp/", "/", "/tmp/../etc/passwd", "/tmp//a", "/a/b/..", "/tmp/a\x00b", "/tmp/a\nb"}
	for _, p := range bad {
		if err := ValidateRemotePath(p); err == nil {
			t.Errorf("expected %q to be rejected", p)
		}
	}
	if err := validateSize(MaxFileSize + 1); err == nil {
		t.Error("expected oversized file to be rejected")
	}
	if err := validateFileName("a/b"); err == nil {
		t.Error("expected path separator in file name to be rejected")
	}
}

// --- upload ---

// The classic operator mistake is entering a directory instead of a full file
// path. Before the guard this transferred the entire file and only failed at the
// final rename with a bare "sftp: Failure".
func TestUploadRejectsDirectoryTargetBeforeSending(t *testing.T) {
	fs := newMemFS()
	fs.dirs["/root"] = true
	m, _ := newTestManager(t, fs, t.TempDir())
	payload := []byte("staging must never be created")
	task := mustCreateUpload(t, m, "/root", int64(len(payload)), true)
	task.FileName = "Windows10.iso"
	_, err := m.WriteChunk(context.Background(), task.ID, 0, bytes.NewReader(payload), int64(len(payload)))
	if err == nil {
		t.Fatal("upload into a directory must fail immediately")
	}
	if !strings.Contains(err.Error(), "目录") {
		t.Fatalf("error should explain that the path is a directory, got %v", err)
	}
	if _, ok := fs.files[task.TempPath]; ok {
		t.Fatalf("staging file must not be created for an impossible target: %s", task.TempPath)
	}
	if _, ok := fs.files["/root"]; ok {
		t.Fatal("the directory must never be replaced by a file")
	}
}

func TestUploadRejectsMissingParentDirectory(t *testing.T) {
	fs := newMemFS()
	m, _ := newTestManager(t, fs, t.TempDir())
	_, err := m.Create(context.Background(), CreateRequest{
		AssetID: "asset-1", Direction: DirectionUpload, RemotePath: "/nope/deep/file.iso", Size: 4, Overwrite: true,
	})
	if err == nil || !strings.Contains(err.Error(), "目录") {
		t.Fatalf("expected a missing-directory error at create time, got %v", err)
	}
}

func TestUploadPublishesOnlyAfterFullSize(t *testing.T) {
	fs := newMemFS()
	m, audit := newTestManager(t, fs, t.TempDir())
	payload := []byte("hello qiyun transfer")
	task := mustCreateUpload(t, m, "/tmp/app.log", int64(len(payload)), false)

	first := int64(8)
	got, err := m.WriteChunk(context.Background(), task.ID, 0, bytes.NewReader(payload[:first]), first)
	if err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	if got.Status != StatusActive || got.Offset != first {
		t.Fatalf("unexpected state after first chunk: %+v", got)
	}
	// The destination must not exist mid-transfer; only the staging file does.
	if _, ok := fs.files["/tmp/app.log"]; ok {
		t.Fatal("destination file appeared before the upload completed")
	}
	if _, ok := fs.files[task.TempPath]; !ok {
		t.Fatal("staging file missing")
	}

	rest := int64(len(payload)) - first
	done, err := m.WriteChunk(context.Background(), task.ID, first, bytes.NewReader(payload[first:]), rest)
	if err != nil {
		t.Fatalf("second chunk: %v", err)
	}
	if done.Status != StatusCompleted || done.Offset != int64(len(payload)) {
		t.Fatalf("upload did not complete: %+v", done)
	}
	if got := string(fs.files["/tmp/app.log"]); got != string(payload) {
		t.Fatalf("published content mismatch: %q", got)
	}
	if _, ok := fs.files[task.TempPath]; ok {
		t.Fatal("staging file should be renamed away")
	}
	if audit.count("transfer.complete") != 1 {
		t.Fatalf("expected one completion audit, got %d", audit.count("transfer.complete"))
	}
	// A late chunk must not append to the published file.
	if _, err := m.WriteChunk(context.Background(), task.ID, done.Offset, bytes.NewReader([]byte("x")), 1); err == nil {
		t.Fatal("expected write after completion to be rejected")
	}
	if got := string(fs.files["/tmp/app.log"]); got != string(payload) {
		t.Fatalf("published file was mutated after completion: %q", got)
	}
}

func TestUploadRejectsOffsetMismatchAndOverflow(t *testing.T) {
	fs := newMemFS()
	m, _ := newTestManager(t, fs, t.TempDir())
	task := mustCreateUpload(t, m, "/tmp/a.bin", 10, false)

	if _, err := m.WriteChunk(context.Background(), task.ID, 5, bytes.NewReader([]byte("abcde")), 5); !errors.Is(err, ErrOffset) {
		t.Fatalf("expected offset error, got %v", err)
	}
	if _, err := m.WriteChunk(context.Background(), task.ID, 0, bytes.NewReader([]byte("abcdefghijk")), 11); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected size error, got %v", err)
	}
	if _, err := m.WriteChunk(context.Background(), task.ID, 0, bytes.NewReader(nil), 0); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected empty chunk rejection, got %v", err)
	}
	// A body shorter than its declared length is a client error: nothing is
	// written, and the task must stay resumable at its confirmed offset.
	if _, err := m.WriteChunk(context.Background(), task.ID, 0, bytes.NewReader([]byte("ab")), 4); err == nil {
		t.Fatal("expected short body to fail")
	}
	if got, _ := m.Get(task.ID); got.Status == StatusFailed || got.Offset != 0 {
		t.Fatalf("short body must not fail the task: %s/%d", got.Status, got.Offset)
	}
	if len(fs.files) != 0 {
		t.Fatalf("short body must not create remote files: %v", fs.files)
	}
}

func TestUploadRefusesSilentOverwrite(t *testing.T) {
	fs := newMemFS()
	fs.files["/tmp/keep.conf"] = []byte("original")
	m, _ := newTestManager(t, fs, t.TempDir())
	if _, err := m.Create(context.Background(), CreateRequest{
		AssetID: "asset-1", Direction: DirectionUpload, RemotePath: "/tmp/keep.conf", Size: 3,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict for existing file, got %v", err)
	}
	if string(fs.files["/tmp/keep.conf"]) != "original" {
		t.Fatal("existing file changed during a rejected create")
	}
	task := mustCreateUpload(t, m, "/tmp/keep.conf", 3, true)
	if _, err := m.WriteChunk(context.Background(), task.ID, 0, bytes.NewReader([]byte("new")), 3); err != nil {
		t.Fatalf("overwrite upload: %v", err)
	}
	if string(fs.files["/tmp/keep.conf"]) != "new" {
		t.Fatal("explicit overwrite did not replace the file")
	}
}

// 部分 SFTP 服务端不提供 posix-rename@openssh.com，覆盖只能退化为先删后改名。
// 这条分支以前没有覆盖，而真实服务器上它恰恰是「传完最后一块才失败」的来源。
func TestUploadOverwritesWhenServerLacksPosixRename(t *testing.T) {
	fs := newMemFS()
	fs.noPosixRename = true
	fs.files["/tmp/keep.conf"] = []byte("original")
	m, _ := newTestManager(t, fs, t.TempDir())
	task := mustCreateUpload(t, m, "/tmp/keep.conf", 3, true)
	done, err := m.WriteChunk(context.Background(), task.ID, 0, bytes.NewReader([]byte("new")), 3)
	if err != nil {
		t.Fatalf("overwrite without posix-rename: %v", err)
	}
	if done.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", done.Status)
	}
	if string(fs.files["/tmp/keep.conf"]) != "new" {
		t.Fatalf("file not replaced, got %q", string(fs.files["/tmp/keep.conf"]))
	}
}

// 未勾覆盖时，即使目标是在创建之后才出现的，也不能把它顶掉。
func TestUploadRefusesOverwriteThatAppearsMidTransfer(t *testing.T) {
	fs := newMemFS()
	m, _ := newTestManager(t, fs, t.TempDir())
	task := mustCreateUpload(t, m, "/tmp/race.conf", 6, false)
	if _, err := m.WriteChunk(context.Background(), task.ID, 0, bytes.NewReader([]byte("abc")), 3); err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	fs.files["/tmp/race.conf"] = []byte("someone else")
	_, err := m.WriteChunk(context.Background(), task.ID, 3, bytes.NewReader([]byte("def")), 3)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if string(fs.files["/tmp/race.conf"]) != "someone else" {
		t.Fatalf("existing file was clobbered: %q", string(fs.files["/tmp/race.conf"]))
	}
}

// 移除一条失败任务后，远端残留的 .part 也必须清掉：记录一旦删除就再也无法续传，
// 留着那些字节只会让远端目录越攒越乱。
func TestForgetRemovesFailedStagingFile(t *testing.T) {
	fs := newMemFS()
	m, _ := newTestManager(t, fs, t.TempDir())
	task := mustCreateUpload(t, m, "/tmp/lost.bin", 6, false)
	if _, err := m.WriteChunk(context.Background(), task.ID, 0, bytes.NewReader([]byte("abc")), 3); err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	m.fail(task.ID, errors.New("链路中断"))
	if !fs.exists(task.TempPath) {
		t.Fatal("失败后临时文件本应保留以便续传")
	}
	if err := m.Forget(task.ID); err != nil {
		t.Fatalf("forget: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !fs.exists(task.TempPath) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("临时文件没有随任务记录一起清理：%s", task.TempPath)
}

func TestUploadRequiresApprovedHostKey(t *testing.T) {
	fs := newMemFS()
	m, _ := newTestManager(t, fs, t.TempDir())
	_, err := m.Create(context.Background(), CreateRequest{
		AssetID: "asset-locked", Direction: DirectionUpload, RemotePath: "/tmp/a", Size: 1,
	})
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "指纹") {
		t.Fatalf("expected host key conflict, got %v", err)
	}
}

func TestUploadResumesAfterRestart(t *testing.T) {
	fs := newMemFS()
	dir := t.TempDir()
	payload := []byte("0123456789abcdef")
	first := int64(6)

	m1, _ := newTestManager(t, fs, dir)
	task := mustCreateUpload(t, m1, "/tmp/big.img", int64(len(payload)), false)
	if _, err := m1.WriteChunk(context.Background(), task.ID, 0, bytes.NewReader(payload[:first]), first); err != nil {
		t.Fatalf("first chunk: %v", err)
	}

	// A restart cannot keep the live handle: the task must come back paused and
	// resumable from its confirmed offset.
	m2, _ := newTestManager(t, fs, dir)
	resumed, err := m2.Get(task.ID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if resumed.Status != StatusPaused || resumed.Offset != first {
		t.Fatalf("expected paused at %d, got %s/%d", first, resumed.Status, resumed.Offset)
	}
	rest := int64(len(payload)) - first
	done, err := m2.WriteChunk(context.Background(), task.ID, first, bytes.NewReader(payload[first:]), rest)
	if err != nil {
		t.Fatalf("resume chunk: %v", err)
	}
	if done.Status != StatusCompleted || string(fs.files["/tmp/big.img"]) != string(payload) {
		t.Fatalf("resume produced wrong result: %+v %q", done, string(fs.files["/tmp/big.img"]))
	}
}

func TestUploadResumeFailsWhenStagingLost(t *testing.T) {
	fs := newMemFS()
	dir := t.TempDir()
	m1, _ := newTestManager(t, fs, dir)
	task := mustCreateUpload(t, m1, "/tmp/x.bin", 4, false)
	if _, err := m1.WriteChunk(context.Background(), task.ID, 0, bytes.NewReader([]byte("ab")), 2); err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	delete(fs.files, task.TempPath)

	m2, _ := newTestManager(t, fs, dir)
	if _, err := m2.WriteChunk(context.Background(), task.ID, 2, bytes.NewReader([]byte("cd")), 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict when staging file is gone, got %v", err)
	}
	if _, ok := fs.files["/tmp/x.bin"]; ok {
		t.Fatal("destination must not exist after a failed resume")
	}
}

func TestCancelRemovesStagingButNotPublishedFile(t *testing.T) {
	fs := newMemFS()
	m, _ := newTestManager(t, fs, t.TempDir())
	task := mustCreateUpload(t, m, "/tmp/c.bin", 8, false)
	if _, err := m.WriteChunk(context.Background(), task.ID, 0, bytes.NewReader([]byte("abcd")), 4); err != nil {
		t.Fatalf("chunk: %v", err)
	}
	canceled, err := m.Cancel(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if canceled.Status != StatusCanceled {
		t.Fatalf("expected canceled, got %s", canceled.Status)
	}
	if _, ok := fs.files[task.TempPath]; ok {
		t.Fatal("staging file should be removed on cancel")
	}
	// Repeating a cancel is idempotent: it must not resurrect or mutate the task.
	again, err := m.Cancel(context.Background(), task.ID)
	if err != nil || again.Status != StatusCanceled {
		t.Fatalf("expected idempotent cancel, got %v %q", err, again.Status)
	}
}

// --- download ---

func TestDownloadServesRangeAndResumes(t *testing.T) {
	fs := newMemFS()
	content := []byte("0123456789abcdefghij")
	fs.files["/tmp/d.bin"] = content
	m, audit := newTestManager(t, fs, t.TempDir())
	task, err := m.Create(context.Background(), CreateRequest{AssetID: "asset-1", Direction: DirectionDownload, RemotePath: "/tmp/d.bin"})
	if err != nil {
		t.Fatalf("create download: %v", err)
	}
	if task.Size != int64(len(content)) {
		t.Fatalf("size mismatch: %d", task.Size)
	}

	reader, _, err := m.OpenDownload(context.Background(), task.ID, 5)
	if err != nil {
		t.Fatalf("open download: %v", err)
	}
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(content[5:]) {
		t.Fatalf("range content mismatch: %q", string(got))
	}
	_ = reader.Close()
	if audit.count("transfer.complete") != 0 {
		t.Fatal("a range read must not mark the download complete")
	}
	// Progress is recorded so a later request can resume from here.
	if progress, _ := m.Get(task.ID); progress.Offset != int64(len(content)) {
		t.Fatalf("expected offset progress, got %d", progress.Offset)
	}

	full, _, err := m.OpenDownload(context.Background(), task.ID, 0)
	if err != nil {
		t.Fatalf("open full: %v", err)
	}
	all, _ := io.ReadAll(full)
	if string(all) != string(content) {
		t.Fatalf("full content mismatch: %q", string(all))
	}
	_ = full.Close()
	if audit.count("transfer.complete") != 0 {
		t.Fatal("reading bytes must not complete the task on its own")
	}
	final, err := m.Complete(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("complete download: %v", err)
	}
	if final.Status != StatusCompleted || audit.count("transfer.complete") != 1 {
		t.Fatalf("expected one completion record, got %s/%d", final.Status, audit.count("transfer.complete"))
	}

	if _, _, err := m.OpenDownload(context.Background(), task.ID, task.Size+1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected out-of-range rejection, got %v", err)
	}
}

func TestDownloadRejectsDirectories(t *testing.T) {
	fs := newMemFS()
	m, _ := newTestManager(t, fs, t.TempDir())
	_, err := m.Create(context.Background(), CreateRequest{AssetID: "asset-1", Direction: DirectionDownload, RemotePath: "/tmp"})
	if !errors.Is(err, ErrRemoteNotFile) {
		t.Fatalf("expected regular-file requirement, got %v", err)
	}
}

func TestParseRange(t *testing.T) {
	cases := []struct {
		header      string
		size        int64
		start, end  int64
		expectError bool
	}{
		{"", 100, 0, 99, false},
		{"bytes=0-9", 100, 0, 9, false},
		{"bytes=50-", 100, 50, 99, false},
		{"bytes=-10", 100, 90, 99, false},
		{"bytes=90-200", 100, 90, 99, false},
		{"bytes=100-", 100, 0, 0, true},
		{"bytes=9-5", 100, 0, 0, true},
		{"bytes=0-1,5-6", 100, 0, 0, true},
		{"items=0-1", 100, 0, 0, true},
		{"bytes=abc-", 100, 0, 0, true},
	}
	for _, c := range cases {
		start, end, err := parseRange(c.header, c.size)
		if c.expectError {
			if err == nil {
				t.Errorf("%q: expected error", c.header)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.header, err)
			continue
		}
		if start != c.start || end != c.end {
			t.Errorf("%q: got %d-%d, want %d-%d", c.header, start, end, c.start, c.end)
		}
	}
}

func TestConcurrentTaskLimit(t *testing.T) {
	fs := newMemFS()
	m, _ := newTestManager(t, fs, t.TempDir()) // MaxTasks = 2
	mustCreateUpload(t, m, "/tmp/1.bin", 4, false)
	mustCreateUpload(t, m, "/tmp/2.bin", 4, false)
	if _, err := m.Create(context.Background(), CreateRequest{
		AssetID: "asset-1", Direction: DirectionUpload, RemotePath: "/tmp/3.bin", Size: 4,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected task limit conflict, got %v", err)
	}
}

func TestUploadRetriesAfterConnectionLost(t *testing.T) {
	fs := newMemFS()
	m, _ := newTestManager(t, fs, t.TempDir())
	payload := []byte("abcdef")
	task := mustCreateUpload(t, m, "/tmp/retry.bin", int64(len(payload)), false)

	// One transport failure mid-chunk must be recovered by reconnecting and
	// rewriting from the same confirmed offset.
	fs.failWrites = 1
	done, err := m.WriteChunk(context.Background(), task.ID, 0, bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if done.Status != StatusCompleted {
		t.Fatalf("expected completed after retry, got %s", done.Status)
	}
	if got := string(fs.files["/tmp/retry.bin"]); got != string(payload) {
		t.Fatalf("content mismatch after retry: %q", got)
	}
}

func TestFailedTaskStaysResumable(t *testing.T) {
	fs := newMemFS()
	m, _ := newTestManager(t, fs, t.TempDir())
	payload := []byte("0123456789")
	task := mustCreateUpload(t, m, "/tmp/resume-failed.bin", int64(len(payload)), false)
	if _, err := m.WriteChunk(context.Background(), task.ID, 0, bytes.NewReader(payload[:4]), 4); err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	// Force a failure that survives both attempts.
	fs.failWrites = 2
	if _, err := m.WriteChunk(context.Background(), task.ID, 4, bytes.NewReader(payload[4:]), 6); err == nil {
		t.Fatal("expected the chunk to fail")
	}
	failed, _ := m.Get(task.ID)
	if failed.Status != StatusFailed || failed.Offset != 4 {
		t.Fatalf("expected failed at offset 4, got %s/%d", failed.Status, failed.Offset)
	}
	// Retrying a failed task must be allowed and must not restart from zero.
	done, err := m.WriteChunk(context.Background(), task.ID, 4, bytes.NewReader(payload[4:]), 6)
	if err != nil {
		t.Fatalf("expected failed task to be resumable, got %v", err)
	}
	if done.Status != StatusCompleted || string(fs.files["/tmp/resume-failed.bin"]) != string(payload) {
		t.Fatalf("resume after failure produced %+v %q", done, string(fs.files["/tmp/resume-failed.bin"]))
	}
}

func TestCreateReusesConnectionForFirstChunk(t *testing.T) {
	fs := newMemFS()
	assets := fakeAssets{items: map[string]store.Asset{
		"asset-1": {ID: "asset-1", Name: "生产主机", HostKey: "ssh-ed25519 AAAA", HostKeyStatus: "verified"},
	}}
	var mu sync.Mutex
	dials := 0
	m, err := New(assets, func(context.Context, string) (Remote, error) {
		mu.Lock()
		dials++
		mu.Unlock()
		return fs, nil
	}, &countingAudit{}, Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	task, err := m.Create(context.Background(), CreateRequest{
		AssetID: "asset-1", Direction: DirectionUpload, RemotePath: "/tmp/one-dial.bin", Size: 3,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := m.WriteChunk(context.Background(), task.ID, 0, bytes.NewReader([]byte("abc")), 3); err != nil {
		t.Fatalf("chunk: %v", err)
	}
	// Creating the task must hand its connection to the session; a second SSH
	// handshake for the first chunk is what made uploads slow on real links.
	mu.Lock()
	defer mu.Unlock()
	if dials != 1 {
		t.Fatalf("expected 1 dial for create+first chunk, got %d", dials)
	}
}
