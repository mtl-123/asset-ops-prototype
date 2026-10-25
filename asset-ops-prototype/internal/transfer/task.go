// Package transfer implements resumable file transfer that is independent from
// the interactive terminal session. Every upload is written to a temporary
// remote file and renamed into place only after the full size is confirmed, so
// an interrupted transfer never replaces a healthy file with a partial one.
package transfer

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DirectionUpload   = "upload"
	DirectionDownload = "download"

	StatusPending   = "pending"
	StatusActive    = "active"
	StatusPaused    = "paused"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCanceled  = "canceled"
)

// MaxChunk bounds one request body. The browser sends sequential chunks, so a
// larger value only increases peak memory without helping throughput.
const MaxChunk = 8 << 20

// MaxFileSize is a sanity bound, not a promise of sustained throughput.
const MaxFileSize = int64(4) << 40 // 4 TiB

const maxRemotePath = 4096
const maxName = 255

var (
	ErrInvalid       = errors.New("invalid transfer request")
	ErrNotFound      = errors.New("transfer not found")
	ErrOffset        = errors.New("chunk offset does not match confirmed offset")
	ErrTooLarge      = errors.New("file or chunk exceeds the allowed size")
	ErrConflict      = errors.New("transfer state does not allow this operation")
	ErrRemoteNotFile = errors.New("remote path is not a regular file")
)

type Task struct {
	ID         string     `json:"id"`
	AssetID    string     `json:"asset_id"`
	AssetName  string     `json:"asset_name,omitempty"`
	Direction  string     `json:"direction"`
	RemotePath string     `json:"remote_path"`
	FileName   string     `json:"file_name"`
	Size       int64      `json:"size"`
	Offset     int64      `json:"offset"`
	Status     string     `json:"status"`
	Error      string     `json:"error,omitempty"`
	Overwrite  bool       `json:"overwrite"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	Completed  *time.Time `json:"completed_at,omitempty"`

	// TempPath is the remote staging file for an upload. It is recomputed on
	// load, and exposed so the UI can point at the partly written file.
	TempPath string `json:"temp_path,omitempty"`
}

func (t Task) Clone() Task {
	c := t
	if t.Completed != nil {
		completed := *t.Completed
		c.Completed = &completed
	}
	return c
}

// ValidateRemotePath accepts only absolute POSIX paths with no traversal
// segments. The remote account already has its own privileges; the goal here is
// to reject malformed or ambiguous input, not to sandbox the user.
func ValidateRemotePath(p string) error {
	if p == "" || len(p) > maxRemotePath {
		return fmt.Errorf("%w: 远端路径为空或过长", ErrInvalid)
	}
	if strings.ContainsAny(p, "\x00\r\n") {
		return fmt.Errorf("%w: 远端路径包含非法字符", ErrInvalid)
	}
	if !strings.HasPrefix(p, "/") {
		return fmt.Errorf("%w: 远端路径必须是绝对路径", ErrInvalid)
	}
	if p == "/" || strings.HasSuffix(p, "/") {
		return fmt.Errorf("%w: 远端路径必须指向文件而不是目录", ErrInvalid)
	}
	for _, segment := range strings.Split(p, "/") {
		if segment == ".." {
			return fmt.Errorf("%w: 远端路径不能包含 ..", ErrInvalid)
		}
	}
	if base := path.Base(p); base == "" || base == "." || base == ".." || len(base) > maxName {
		return fmt.Errorf("%w: 远端文件名不合法", ErrInvalid)
	}
	// A clean path must round-trip; this rejects duplicate slashes and similar
	// inputs that could resolve to a different file than the user expects.
	if path.Clean(p) != p {
		return fmt.Errorf("%w: 远端路径需要规范化（多余斜杠或相对片段）", ErrInvalid)
	}
	return nil
}

func validateFileName(name string) error {
	if name == "" || len(name) > maxName {
		return fmt.Errorf("%w: 文件名不合法", ErrInvalid)
	}
	if strings.ContainsAny(name, "\x00/\\\r\n") {
		return fmt.Errorf("%w: 文件名包含非法字符", ErrInvalid)
	}
	return nil
}

func validateSize(size int64) error {
	if size < 0 || size > MaxFileSize {
		return fmt.Errorf("%w: 文件大小超出允许范围", ErrTooLarge)
	}
	return nil
}

func newID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// stagingPath keeps the temporary file in the destination directory so the final
// rename stays on the same filesystem and is therefore atomic.
// The staging name is deliberately not a dotfile: users check the remote
// directory while a long upload runs, and a hidden file looks like a failure.
func stagingPath(remotePath, id string) string {
	if len(id) > 8 {
		id = id[:8]
	}
	return path.Join(path.Dir(remotePath), path.Base(remotePath)+".qiyun-"+id+".part")
}

type taskStore struct {
	mu    sync.Mutex
	dir   string
	tasks map[string]*Task
	now   func() time.Time
}

func newTaskStore(dir string) (*taskStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &taskStore{dir: dir, tasks: map[string]*Task{}, now: time.Now}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(path.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var t Task
		if json.Unmarshal(raw, &t) != nil || t.ID == "" {
			continue
		}
		// A restart cannot keep a live handle, so an in-flight task becomes
		// paused and stays resumable from its confirmed offset.
		if t.Status == StatusActive || t.Status == StatusPending {
			t.Status = StatusPaused
		}
		if t.Direction == DirectionUpload && t.Status != StatusCanceled && t.Status != StatusCompleted {
			t.TempPath = stagingPath(t.RemotePath, t.ID)
		}
		s.tasks[t.ID] = &t
	}
	return s, nil
}

func (s *taskStore) persist(t *Task) error {
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	tmp := path.Join(s.dir, t.ID+".json.tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path.Join(s.dir, t.ID+".json"))
}

func (s *taskStore) add(t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.persist(t); err != nil {
		return err
	}
	s.tasks[t.ID] = t
	return nil
}

func (s *taskStore) save(t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.persist(t); err != nil {
		return err
	}
	s.tasks[t.ID] = t
	return nil
}

func (s *taskStore) delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tasks, id)
	_ = os.Remove(path.Join(s.dir, id+".json"))
}

// update mutates a task while holding the store lock, so a returned snapshot is
// never half-written by a concurrent request.
func (s *taskStore) update(id string, fn func(*Task) error) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return Task{}, ErrNotFound
	}
	if err := fn(t); err != nil {
		return Task{}, err
	}
	t.UpdatedAt = s.now().UTC()
	if err := s.persist(t); err != nil {
		return Task{}, err
	}
	return t.Clone(), nil
}

func (s *taskStore) get(id string) (Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return Task{}, false
	}
	return t.Clone(), true
}

func (s *taskStore) list(assetID string) []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		if assetID != "" && t.AssetID != assetID {
			continue
		}
		// Canceled tasks carry no resumable value and would otherwise pile up.
		if t.Status == StatusCanceled {
			continue
		}
		out = append(out, t.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}
