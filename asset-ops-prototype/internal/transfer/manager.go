package transfer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"qiyun-asset-ops/internal/store"
)

// RemoteFile is the subset of remote file behaviour the manager needs. Both the
// SFTP adapter and the in-memory test double implement it.
type RemoteFile interface {
	io.ReaderAt
	io.WriterAt
	io.Closer
	// Truncate discards bytes past size. Concurrent writes can leave a longer
	// file after an error, so recovery truncates back to the confirmed offset.
	Truncate(size int64) error
}

// Remote hides the transport so transfer logic can be tested without SSH.
type Remote interface {
	Stat(name string) (os.FileInfo, error)
	MkdirAll(dir string) error
	Open(name string) (RemoteFile, error)
	OpenFile(name string, flag int) (RemoteFile, error)
	Create(name string) (RemoteFile, error)
	// Rename is plain SFTP v3 rename: it refuses to replace an existing file.
	Rename(oldpath, newpath string) error
	// PosixRename uses posix-rename@openssh.com and does replace the target.
	PosixRename(oldpath, newpath string) error
	// SupportsPosixRename reports whether the server offers that extension.
	SupportsPosixRename() bool
	Remove(name string) error
	Close() error
}

type RemoteFactory func(ctx context.Context, assetID string) (Remote, error)

type AssetProvider interface {
	GetAsset(ctx context.Context, id string) (store.Asset, error)
}

type Auditor interface {
	Audit(ctx context.Context, action, objectType, objectID, result string, metadata any)
}

type Options struct {
	DataDir     string
	MaxTasks    int
	IdleTimeout time.Duration
}

type CreateRequest struct {
	AssetID    string
	Direction  string
	RemotePath string
	FileName   string
	Size       int64
	Overwrite  bool
}

type Manager struct {
	store    *taskStore
	assets   AssetProvider
	newRe    RemoteFactory
	audit    Auditor
	now      func() time.Time
	maxTasks int
	idle     time.Duration

	mu       sync.Mutex
	sessions map[string]*session
	locks    map[string]*sync.Mutex
}

func New(assets AssetProvider, newRemote RemoteFactory, audit Auditor, opts Options) (*Manager, error) {
	if assets == nil || newRemote == nil {
		return nil, fmt.Errorf("transfer: asset provider and remote factory are required")
	}
	if strings.TrimSpace(opts.DataDir) == "" {
		return nil, fmt.Errorf("transfer: data directory is required")
	}
	st, err := newTaskStore(filepath.ToSlash(filepath.Join(opts.DataDir, "transfers")))
	if err != nil {
		return nil, err
	}
	if opts.MaxTasks <= 0 {
		opts.MaxTasks = 4
	}
	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = 5 * time.Minute
	}
	m := &Manager{
		store: st, assets: assets, newRe: newRemote, audit: audit,
		now: time.Now, maxTasks: opts.MaxTasks, idle: opts.IdleTimeout,
		sessions: map[string]*session{}, locks: map[string]*sync.Mutex{},
	}
	m.reapOnce()
	go m.reapLoop()
	return m, nil
}

type session struct {
	mu       sync.Mutex
	remote   Remote
	file     RemoteFile
	filePath string
	used     time.Time
	// Split accounting for one task: how much of the wall clock went into
	// receiving the bytes from the client, and how much into pushing them to the
	// remote host. Without this split a "transfer is slow" report cannot be
	// attributed to the browser hop or to the SSH link.
	bodyNanos  atomic.Int64
	writeNanos atomic.Int64
	chunks     atomic.Int64
}

func (m *Manager) lockFor(id string) func() {
	m.mu.Lock()
	l, ok := m.locks[id]
	if !ok {
		l = &sync.Mutex{}
		m.locks[id] = l
	}
	m.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (m *Manager) sessionFor(id string) *session {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		s = &session{}
		m.sessions[id] = s
	}
	s.used = m.now()
	return s
}

func (m *Manager) dropSession(id string) {
	m.mu.Lock()
	s := m.sessions[id]
	delete(m.sessions, id)
	// The per-task mutex is intentionally kept: removing it here would let a
	// concurrent caller create a second mutex and enter the same task twice.
	m.mu.Unlock()
	if s != nil {
		s.close()
	}
}

func (s *session) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
	if s.remote != nil {
		_ = s.remote.Close()
		s.remote = nil
	}
}

func (m *Manager) reapOnce() {
	limit := m.now().Add(-m.idle)
	var stale []string
	m.mu.Lock()
	for id, s := range m.sessions {
		if s.used.Before(limit) {
			stale = append(stale, id)
		}
	}
	m.mu.Unlock()
	for _, id := range stale {
		m.dropSession(id)
	}
}

func (m *Manager) reapLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		m.reapOnce()
	}
}

// List returns resumable and finished transfers, newest first. Canceled tasks
// are hidden because they carry no resumable state.
func (m *Manager) List(assetID string) []Task { return m.store.list(assetID) }

func (m *Manager) Get(id string) (Task, error) {
	t, ok := m.store.get(id)
	if !ok {
		return Task{}, ErrNotFound
	}
	return t, nil
}

func (m *Manager) activeTasks() int {
	n := 0
	for _, t := range m.store.list("") {
		if t.Status == StatusActive || t.Status == StatusPending || t.Status == StatusPaused {
			n++
		}
	}
	return n
}

func (m *Manager) Create(ctx context.Context, req CreateRequest) (Task, error) {
	if req.Direction != DirectionUpload && req.Direction != DirectionDownload {
		return Task{}, fmt.Errorf("%w: 方向必须是 upload 或 download", ErrInvalid)
	}
	if err := ValidateRemotePath(req.RemotePath); err != nil {
		return Task{}, err
	}
	asset, err := m.assets.GetAsset(ctx, req.AssetID)
	if err != nil {
		return Task{}, err
	}
	if asset.HostKey == "" {
		return Task{}, fmt.Errorf("%w: 请先扫描并批准 SSH 主机指纹", ErrConflict)
	}
	if m.activeTasks() >= m.maxTasks {
		return Task{}, fmt.Errorf("%w: 同时进行的传输任务已达上限（%d），请先完成或取消其他任务", ErrConflict, m.maxTasks)
	}
	remote, err := m.newRe(ctx, req.AssetID)
	if err != nil {
		return Task{}, err
	}
	// The connection is handed to the task session below so the first chunk does
	// not pay a second SSH handshake (auth alone can take seconds on slow links).
	handedOver := false
	defer func() {
		if !handedOver {
			_ = remote.Close()
		}
	}()

	now := m.now().UTC()
	t := Task{
		ID: newID(), AssetID: req.AssetID, AssetName: asset.Name, Direction: req.Direction,
		RemotePath: req.RemotePath, FileName: req.FileName, Status: StatusPending,
		Overwrite: req.Overwrite, CreatedAt: now, UpdatedAt: now,
	}
	if t.FileName == "" {
		t.FileName = path.Base(req.RemotePath)
	}
	if err := validateFileName(t.FileName); err != nil {
		return Task{}, err
	}

	switch req.Direction {
	case DirectionUpload:
		if err := validateSize(req.Size); err != nil {
			return Task{}, err
		}
		t.Size = req.Size
		parent := path.Dir(req.RemotePath)
		info, err := remote.Stat(parent)
		if err != nil {
			return Task{}, fmt.Errorf("%w: 远端目录不可访问：%v", ErrConflict, err)
		}
		if !info.IsDir() {
			return Task{}, fmt.Errorf("%w: 远端父路径不是目录", ErrInvalid)
		}
		if !req.Overwrite {
			if _, err := remote.Stat(req.RemotePath); err == nil {
				return Task{}, fmt.Errorf("%w: 远端已存在同名文件，请确认覆盖后重试", ErrConflict)
			}
		}
		t.TempPath = stagingPath(req.RemotePath, t.ID)
	case DirectionDownload:
		info, err := remote.Stat(req.RemotePath)
		if err != nil {
			return Task{}, fmt.Errorf("%w: 远端文件不可访问：%v", ErrConflict, err)
		}
		if !info.Mode().IsRegular() {
			return Task{}, ErrRemoteNotFile
		}
		if err := validateSize(info.Size()); err != nil {
			return Task{}, err
		}
		t.Size = info.Size()
		t.Status = StatusActive
	}

	if err := m.store.add(&t); err != nil {
		return Task{}, err
	}
	s := m.sessionFor(t.ID)
	s.mu.Lock()
	s.remote, s.file = remote, nil
	s.mu.Unlock()
	handedOver = true
	m.audit.Audit(ctx, "transfer.create", "transfer", t.ID, "success", map[string]any{
		"asset_id": t.AssetID, "direction": t.Direction, "remote_path": t.RemotePath, "size": t.Size,
	})
	return t.Clone(), nil
}

// WriteChunk appends one verified chunk at the confirmed offset. The offset must
// match exactly, so a retried or reordered request cannot corrupt the file.
func (m *Manager) WriteChunk(ctx context.Context, id string, offset int64, body io.Reader, size int64) (Task, error) {
	unlock := m.lockFor(id)
	defer unlock()

	task, ok := m.store.get(id)
	if !ok {
		return Task{}, ErrNotFound
	}
	if task.Direction != DirectionUpload {
		return Task{}, fmt.Errorf("%w: 该任务不是上传任务", ErrConflict)
	}
	// A failed task stays resumable: the confirmed offset is still valid and the
	// staging file is only trimmed back to it, so retrying is safe.
	if task.Status == StatusCompleted || task.Status == StatusCanceled {
		return Task{}, fmt.Errorf("%w: 任务已结束（%s）", ErrConflict, task.Status)
	}
	if size <= 0 || size > MaxChunk {
		return Task{}, fmt.Errorf("%w: 单个分块必须在 1 字节到 %d 字节之间", ErrTooLarge, MaxChunk)
	}
	if offset != task.Offset {
		return Task{}, fmt.Errorf("%w: 期望 %d，收到 %d", ErrOffset, task.Offset, offset)
	}
	if task.Offset+size > task.Size {
		return Task{}, fmt.Errorf("%w: 分块超出声明的文件大小", ErrTooLarge)
	}
	if err := validateSize(task.Size); err != nil {
		return Task{}, err
	}

	// Buffer the chunk before touching the remote: a consumed request body cannot
	// be replayed, and one reconnect retry needs the bytes again. The size is
	// bounded by MaxChunk, so this stays flat regardless of file size.
	readStart := time.Now()
	data, err := readChunk(body, size)
	if err != nil {
		return Task{}, err
	}
	bodyDur := time.Since(readStart)
	// The per-task lock above already serialises this task's file access; taking
	// the session lock as well would deadlock once a chunk completes the upload.
	writeStart := time.Now()
	werr := m.writeWithRetry(ctx, id, &task, data)
	writeDur := time.Since(writeStart)
	if s := m.sessionFor(id); s != nil {
		s.bodyNanos.Add(bodyDur.Nanoseconds())
		s.writeNanos.Add(writeDur.Nanoseconds())
		s.chunks.Add(1)
	}
	if werr != nil {
		m.dropSession(id)
		m.fail(id, werr)
		slog.Warn("transfer chunk failed", "transfer_id", id, "asset_id", task.AssetID, "offset", offset, "size", size, "error", werr)
		return Task{}, werr
	}
	next := offset + size
	updated, err := m.store.update(id, func(t *Task) error {
		t.Offset = next
		t.Status = StatusActive
		t.Error = ""
		return nil
	})
	if err != nil {
		return Task{}, err
	}
	if next == updated.Size {
		return m.finishUpload(ctx, updated)
	}
	return updated, nil
}

func readChunk(body io.Reader, size int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, MaxChunk+1))
	if err != nil {
		return nil, fmt.Errorf("%w: 读取请求体失败：%v", ErrInvalid, err)
	}
	if int64(len(data)) != size {
		return nil, fmt.Errorf("%w: 请求体 %d 字节与声明的 %d 不一致", ErrInvalid, len(data), size)
	}
	return data, nil
}

// writeWithRetry writes the chunk, and on a transport error discards everything
// past the confirmed offset before reconnecting once. Concurrent SFTP writes can
// leave a longer file after a failure, so the trim is what makes the retry safe.
func (m *Manager) writeWithRetry(ctx context.Context, id string, task *Task, data []byte) error {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		s := m.sessionFor(id)
		if err := m.ensureUploadFile(ctx, s, task); err != nil {
			return err
		}
		_, err := copyAt(s.file, bytes.NewReader(data), task.Offset, int64(len(data)))
		if err == nil {
			return nil
		}
		lastErr = err
		// Recover: close the broken session, reopen, and trim back to the
		// confirmed offset so holes from the failed write cannot survive.
		m.dropSession(id)
		if attempt == 0 {
			continue
		}
	}
	return lastErr
}

func (m *Manager) ensureUploadFile(ctx context.Context, s *session, t *Task) error {
	if s.file != nil {
		return nil
	}
	if s.remote == nil {
		remote, err := m.newRe(ctx, t.AssetID)
		if err != nil {
			return err
		}
		s.remote = remote
	}
	if t.Offset == 0 {
		// Check the destination before sending anything. Passing a directory as the
		// remote path used to cost the whole upload: every byte transferred fine and
		// only the final rename failed with a bare "sftp: Failure".
		if err := guardUploadTarget(s.remote, t); err != nil {
			return err
		}
		f, err := s.remote.Create(t.TempPath)
		if err != nil {
			return fmt.Errorf("创建远端临时文件失败：%w", err)
		}
		s.file, s.filePath = f, t.TempPath
		return nil
	}
	// Resuming: the staged file must still exist with at least the confirmed
	// length, otherwise continuing would silently produce a corrupt file.
	info, err := s.remote.Stat(t.TempPath)
	if err != nil {
		return fmt.Errorf("%w: 断点临时文件已不存在，请重新开始上传", ErrConflict)
	}
	if info.Size() < t.Offset {
		return fmt.Errorf("%w: 临时文件长度小于已确认断点，请重新开始上传", ErrConflict)
	}
	// Open read-write: sftp.Open is read-only, so resuming through it made every
	// WriteAt fail. Truncate also drops a tail left by an interrupted chunk.
	f, err := s.remote.OpenFile(t.TempPath, os.O_RDWR)
	if err != nil {
		return fmt.Errorf("打开远端临时文件失败：%w", err)
	}
	if info.Size() != t.Offset {
		if err := f.Truncate(t.Offset); err != nil {
			_ = f.Close()
			return fmt.Errorf("回退远端临时文件到断点失败：%w", err)
		}
	}
	s.file, s.filePath = f, t.TempPath
	return nil
}

func copyAt(dst io.WriterAt, src io.Reader, offset, size int64) (int64, error) {
	const bufSize = 1 << 20
	buf := make([]byte, bufSize)
	var written int64
	for written < size {
		want := int64(bufSize)
		if remaining := size - written; remaining < want {
			want = remaining
		}
		n, readErr := io.ReadFull(src, buf[:want])
		if n > 0 {
			w, writeErr := dst.WriteAt(buf[:n], offset+written)
			written += int64(w)
			if writeErr != nil {
				return written, fmt.Errorf("写入远端失败：%w", writeErr)
			}
			if w != n {
				return written, fmt.Errorf("%w: 远端短写", ErrConflict)
			}
		}
		if readErr != nil {
			if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
				if written < size {
					return written, fmt.Errorf("%w: 请求体短于声明的分块长度", ErrInvalid)
				}
				break
			}
			return written, readErr
		}
	}
	return written, nil
}

func (m *Manager) finishUpload(ctx context.Context, task Task) (Task, error) {
	s := m.sessionFor(task.ID)
	if s.file != nil {
		if err := s.file.Close(); err != nil {
			m.fail(task.ID, err)
			return Task{}, fmt.Errorf("关闭远端临时文件失败：%w", err)
		}
		s.file = nil
	}
	if s.remote == nil {
		remote, err := m.newRe(ctx, task.AssetID)
		if err != nil {
			m.fail(task.ID, err)
			return Task{}, err
		}
		s.remote = remote
	}
	// Verify the staged length before publishing; a truncated staging file must
	// never replace the destination.
	info, err := s.remote.Stat(task.TempPath)
	if err != nil {
		m.fail(task.ID, err)
		return Task{}, fmt.Errorf("校验远端临时文件失败：%w", err)
	}
	if info.Size() != task.Size {
		err = fmt.Errorf("%w: 临时文件大小 %d 与声明 %d 不一致", ErrConflict, info.Size(), task.Size)
		m.fail(task.ID, err)
		return Task{}, err
	}
	if err := m.publish(s, task); err != nil {
		m.fail(task.ID, err)
		return Task{}, err
	}
	completed := m.now().UTC()
	updated, err := m.store.update(task.ID, func(t *Task) error {
		t.Status = StatusCompleted
		t.Completed = &completed
		t.Error = ""
		return nil
	})
	m.dropSession(task.ID)
	if err != nil {
		return Task{}, err
	}
	m.audit.Audit(ctx, "transfer.complete", "transfer", task.ID, "success", map[string]any{
		"asset_id": task.AssetID, "direction": task.Direction, "remote_path": task.RemotePath, "size": task.Size,
	})
	// One summary line per finished upload: a "transfer is slow" report can be
	// answered from this plus the per-chunk request durations, without replaying
	// the whole chunk log by hand. body_ms is the browser->service hop (loopback
	// unless the service is reached remotely); write_ms is the SSH/SFTP link,
	// which is what actually limits a normal upload.
	elapsed := m.now().Sub(task.CreatedAt)
	rate := 0.0
	if elapsed > 0 {
		rate = float64(task.Size) / (1 << 20) / elapsed.Seconds()
	}
	slog.Info("transfer upload completed", "transfer_id", task.ID, "asset_id", task.AssetID,
		"remote_path", task.RemotePath, "bytes", task.Size,
		"elapsed_ms", elapsed.Milliseconds(), "mib_per_sec", float64(int(rate*10))/10,
		"chunks", s.chunks.Load(),
		"body_ms", s.bodyNanos.Load()/int64(time.Millisecond),
		"write_ms", s.writeNanos.Load()/int64(time.Millisecond))
	return updated, nil
}

// guardUploadTarget rejects destinations that cannot work, using one round trip.
// The common operator mistake is entering a directory ("/root") instead of a file
// ("/root/Windows10.iso"); rename onto a directory reports only SSH_FX_FAILURE,
// which says nothing useful after a multi-hour upload.
func guardUploadTarget(remote Remote, t *Task) error {
	suggestion := path.Join(t.RemotePath, t.FileName)
	if t.FileName == "" {
		suggestion = t.RemotePath + "/<文件名>"
	}
	info, err := remote.Stat(t.RemotePath)
	switch {
	case err == nil && info.IsDir():
		return fmt.Errorf("%w: 远端路径 %s 是一个目录，请填写完整文件路径（例如 %s）", ErrInvalid, t.RemotePath, suggestion)
	case err == nil:
		// Existing file: handled by the overwrite/conflict rules at publish time.
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("无法确认远端目标 %s：%w", t.RemotePath, err)
	}
	dir := path.Dir(t.RemotePath)
	if dir == "." || dir == "/" {
		return nil
	}
	parent, err := remote.Stat(dir)
	if err != nil {
		return fmt.Errorf("%w: 远端目录 %s 不存在或不可访问", ErrInvalid, dir)
	}
	if !parent.IsDir() {
		return fmt.Errorf("%w: 远端路径 %s 的上级 %s 不是目录", ErrInvalid, t.RemotePath, dir)
	}
	return nil
}

// publish moves the verified staging file onto its final name. Overwriting needs
// posix-rename@openssh.com: plain SFTP v3 rename refuses to replace an existing
// file, which is why "允许覆盖" used to fail at the very last step — after every
// byte had been transferred — with a bare "sftp: Failure". Without the extension
// the old file is removed first, so the guarantee degrades from atomic to
// best-effort instead of the upload silently never landing.
func (m *Manager) publish(s *session, task Task) error {
	// Re-check the destination: a task created before this guard existed, or one
	// resumed later, may still point at a directory.
	if err := guardUploadTarget(s.remote, &task); err != nil {
		return err
	}
	if !task.Overwrite {
		// Plain SFTP rename refusing to replace is exactly the guarantee wanted
		// here; the Stat only turns a bare "Failure" into a readable reason.
		if _, err := s.remote.Stat(task.RemotePath); err == nil {
			return fmt.Errorf("%w: 远端已存在同名文件，未覆盖", ErrConflict)
		}
		return wrapPublish(s.remote.Rename(task.TempPath, task.RemotePath))
	}
	if s.remote.SupportsPosixRename() {
		return wrapPublish(s.remote.PosixRename(task.TempPath, task.RemotePath))
	}
	if err := s.remote.Remove(task.RemotePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("覆盖前删除远端旧文件失败：%w", err)
	}
	return wrapPublish(s.remote.Rename(task.TempPath, task.RemotePath))
}

func wrapPublish(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("发布远端文件失败（临时文件仍在，可重新提交续传）：%w", err)
}

func (m *Manager) fail(id string, err error) {
	if _, uerr := m.store.update(id, func(t *Task) error {
		t.Status = StatusFailed
		t.Error = err.Error()
		return nil
	}); uerr != nil {
		slog.Warn("mark transfer failed", "transfer_id", id, "error", uerr)
	}
}

// Complete publishes an upload that reached its declared size, or marks a fully
// downloaded task as finished.
func (m *Manager) Complete(ctx context.Context, id string) (Task, error) {
	unlock := m.lockFor(id)
	defer unlock()
	task, ok := m.store.get(id)
	if !ok {
		return Task{}, ErrNotFound
	}
	if task.Status == StatusCompleted {
		return task, nil
	}
	if task.Direction == DirectionDownload {
		if task.Offset < task.Size {
			return Task{}, fmt.Errorf("%w: 下载尚未完成（%d/%d）", ErrConflict, task.Offset, task.Size)
		}
		completed := m.now().UTC()
		updated, err := m.store.update(id, func(t *Task) error {
			t.Status = StatusCompleted
			t.Completed = &completed
			return nil
		})
		if err == nil {
			m.audit.Audit(ctx, "transfer.complete", "transfer", id, "success", map[string]any{"direction": task.Direction, "remote_path": task.RemotePath, "size": task.Size})
		}
		return updated, err
	}
	if task.Offset != task.Size {
		return Task{}, fmt.Errorf("%w: 上传尚未完成（%d/%d）", ErrConflict, task.Offset, task.Size)
	}
	return m.finishUpload(ctx, task)
}

// Cancel stops a task and removes its staging file. A completed upload is never
// touched, so cancel cannot delete the published file.
func (m *Manager) Cancel(ctx context.Context, id string) (Task, error) {
	unlock := m.lockFor(id)
	defer unlock()
	task, ok := m.store.get(id)
	if !ok {
		return Task{}, ErrNotFound
	}
	if task.Status == StatusCompleted {
		return Task{}, fmt.Errorf("%w: 已完成的任务不能取消", ErrConflict)
	}
	m.dropSession(id)
	if task.Direction == DirectionUpload && task.TempPath != "" {
		if remote, err := m.newRe(ctx, task.AssetID); err == nil {
			_ = remote.Remove(task.TempPath)
			_ = remote.Close()
		}
	}
	updated, err := m.store.update(id, func(t *Task) error {
		t.Status = StatusCanceled
		return nil
	})
	if err != nil {
		return Task{}, err
	}
	m.audit.Audit(ctx, "transfer.cancel", "transfer", id, "success", map[string]any{"asset_id": task.AssetID, "direction": task.Direction, "offset": task.Offset, "size": task.Size})
	return updated, nil
}

// Forget removes a finished or failed task record after its staging file is gone.
func (m *Manager) Forget(id string) error {
	task, ok := m.store.get(id)
	if !ok {
		return ErrNotFound
	}
	if task.Status != StatusCompleted && task.Status != StatusCanceled && task.Status != StatusFailed {
		return fmt.Errorf("%w: 进行中的任务不能删除", ErrConflict)
	}
	m.dropSession(id)
	m.store.delete(id)
	// A failed upload leaves a staging file behind. Once the record is gone there
	// is nothing left to resume from, so keeping the bytes would only strand them
	// on the remote host forever. Best effort and off the request path: the host
	// is often unreachable exactly when a task was forgotten.
	if task.Direction == DirectionUpload && task.TempPath != "" && task.Status != StatusCompleted {
		go func(assetID, temp string) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			remote, err := m.newRe(ctx, assetID)
			if err != nil {
				slog.Debug("skip staging cleanup", "asset_id", assetID, "path", temp, "error", err)
				return
			}
			defer remote.Close()
			if err := remote.Remove(temp); err != nil && !errors.Is(err, os.ErrNotExist) {
				slog.Debug("staging cleanup failed", "asset_id", assetID, "path", temp, "error", err)
			}
		}(task.AssetID, task.TempPath)
	}
	return nil
}

// OpenDownload streams the remote file from start. The returned reader closes the
// remote session when the stream ends, so a browser abort cannot leak a channel.
func (m *Manager) OpenDownload(ctx context.Context, id string, start int64) (io.ReadCloser, Task, error) {
	unlock := m.lockFor(id)
	defer unlock()
	task, ok := m.store.get(id)
	if !ok {
		return nil, Task{}, ErrNotFound
	}
	if task.Direction != DirectionDownload {
		return nil, Task{}, fmt.Errorf("%w: 该任务不是下载任务", ErrConflict)
	}
	if start < 0 || start > task.Size {
		return nil, Task{}, fmt.Errorf("%w: 起始偏移超出文件范围", ErrInvalid)
	}
	s := m.sessionFor(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remote == nil {
		remote, err := m.newRe(ctx, task.AssetID)
		if err != nil {
			return nil, Task{}, err
		}
		s.remote = remote
	}
	if s.file == nil {
		f, err := s.remote.Open(task.RemotePath)
		if err != nil {
			m.dropSession(id)
			return nil, Task{}, fmt.Errorf("打开远端文件失败：%w", err)
		}
		s.file = f
	}
	r := &downloadReader{m: m, taskID: id, file: s.file, off: start, end: task.Size}
	return r, task, nil
}

type downloadReader struct {
	m      *Manager
	taskID string
	file   RemoteFile
	off    int64
	end    int64
	once   sync.Once
}

func (r *downloadReader) Read(p []byte) (int, error) {
	if r.off >= r.end {
		return 0, io.EOF
	}
	if remaining := r.end - r.off; int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := r.file.ReadAt(p, r.off)
	if n > 0 {
		r.off += int64(n)
		return n, nil
	}
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	return 0, err
}

// Close records progress but never declares the download finished. A byte range
// that happens to reach the end does not prove the client stored the beginning,
// so only an explicit Complete (after the client verified its local file) may
// mark the task completed.
func (r *downloadReader) Close() error {
	var err error
	r.once.Do(func() {
		if _, uerr := r.m.store.update(r.taskID, func(t *Task) error {
			if r.off > t.Offset {
				t.Offset = r.off
			}
			if t.Status != StatusCompleted && t.Status != StatusCanceled {
				t.Status = StatusPaused
			}
			return nil
		}); uerr != nil {
			err = uerr
		}
		r.m.dropSession(r.taskID)
	})
	return err
}

// --- SFTP adapter ---

type sftpRemote struct {
	client *sftp.Client
	ssh    *ssh.Client
	done   chan struct{}
	once   sync.Once
}

func (r *sftpRemote) Stat(name string) (os.FileInfo, error) { return r.client.Stat(name) }
func (r *sftpRemote) MkdirAll(dir string) error             { return r.client.MkdirAll(dir) }
func (r *sftpRemote) Open(name string) (RemoteFile, error) {
	f, err := r.client.Open(name)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// OpenFile is required for resumable writes; Open alone is read-only.
func (r *sftpRemote) OpenFile(name string, flag int) (RemoteFile, error) {
	f, err := r.client.OpenFile(name, flag)
	if err != nil {
		return nil, err
	}
	return f, nil
}
func (r *sftpRemote) Create(name string) (RemoteFile, error) {
	f, err := r.client.Create(name)
	if err != nil {
		return nil, err
	}
	return f, nil
}
func (r *sftpRemote) Rename(oldpath, newpath string) error { return r.client.Rename(oldpath, newpath) }
func (r *sftpRemote) PosixRename(oldpath, newpath string) error {
	return r.client.PosixRename(oldpath, newpath)
}
func (r *sftpRemote) SupportsPosixRename() bool {
	_, ok := r.client.HasExtension("posix-rename@openssh.com")
	return ok
}
func (r *sftpRemote) Remove(name string) error { return r.client.Remove(name) }
func (r *sftpRemote) Close() error {
	r.once.Do(func() {
		if r.done != nil {
			close(r.done)
		}
	})
	var first error
	if r.client != nil {
		if err := r.client.Close(); err != nil {
			first = err
		}
	}
	if r.ssh != nil {
		if err := r.ssh.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// SFTPFactory builds sessions through the existing host-key-verified dialer.
func SFTPFactory(dial interface {
	Dial(ctx context.Context, assetID string) (*ssh.Client, store.Asset, string, error)
}) RemoteFactory {
	return func(ctx context.Context, assetID string) (Remote, error) {
		client, _, _, err := dial.Dial(ctx, assetID)
		if err != nil {
			return nil, err
		}
		// Concurrent writes pipeline packets instead of paying one round trip per
		// 32 KiB, which is what made a 79 MB upload crawl over a real link.
		sc, err := sftp.NewClient(client,
			sftp.UseConcurrentWrites(true),
			sftp.UseConcurrentReads(true),
			sftp.MaxConcurrentRequestsPerFile(64),
		)
		if err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("远端未启用 SFTP：%w", err)
		}
		remote := &sftpRemote{client: sc, ssh: client, done: make(chan struct{})}
		go remote.keepAlive()
		return remote, nil
	}
}

// keepAlive stops idle NAT/firewall entries from dropping a long transfer.
func (r *sftpRemote) keepAlive() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-ticker.C:
			if _, _, err := r.ssh.SendRequest("keepalive@openssh.com", true, nil); err != nil {
				return
			}
		}
	}
}
