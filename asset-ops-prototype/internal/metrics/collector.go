package metrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"qiyun-asset-ops/internal/store"
)

// Status describes the last collection attempt for one host.
const (
	StatusOK      = "ok"
	StatusError   = "error"
	StatusPending = "pending"
)

// AssetProvider lists the assets to sample. Empty query returns everything.
type AssetProvider interface {
	ListAssets(ctx context.Context, query string) ([]store.Asset, error)
}

// Session is one remote shell able to run the collection script. Keeping the
// collector behind this seam means parsing/scheduling can be tested without an
// SSH server, and the SSH wiring lives in one adapter.
type Session interface {
	Run(ctx context.Context, script string) (string, error)
	Close() error
}

// Dialer opens a session for the stored asset credential.
type Dialer interface {
	Open(ctx context.Context, assetID string) (Session, error)
}

// SSHDialer adapts sshgateway.Manager (host-key verified) to the collector.
func SSHDialer(dial interface {
	Dial(ctx context.Context, assetID string) (*ssh.Client, store.Asset, string, error)
}) Dialer {
	return sshDialer{dial: dial}
}

type sshDialer struct {
	dial interface {
		Dial(ctx context.Context, assetID string) (*ssh.Client, store.Asset, string, error)
	}
}

func (d sshDialer) Open(ctx context.Context, assetID string) (Session, error) {
	client, _, _, err := d.dial.Dial(ctx, assetID)
	if err != nil {
		return nil, err
	}
	return &sshSession{client: client}, nil
}

type sshSession struct{ client *ssh.Client }

func (s *sshSession) Run(ctx context.Context, script string) (string, error) {
	return runScript(ctx, s.client, script)
}

func (s *sshSession) Close() error {
	if s.client == nil {
		return nil
	}
	return s.client.Close()
}

// Options tunes the poller.
type Options struct {
	DataDir     string
	Interval    time.Duration // 0 disables the background poller
	Timeout     time.Duration
	Concurrency int
	History     int
}

// Record is the last known state of one host.
type Record struct {
	AssetID     string    `json:"asset_id"`
	Status      string    `json:"status"`
	Error       string    `json:"error,omitempty"`
	Snapshot    *Snapshot `json:"snapshot,omitempty"`
	CollectedAt time.Time `json:"collected_at,omitempty"`
	DurationMS  int64     `json:"duration_ms"`
	// NextAttempt and Failures are internal scheduling state, not API surface.
	NextAttempt time.Time `json:"-"`
	Failures    int       `json:"-"`
}

// Collector samples Linux hosts over SSH and keeps the latest result per asset.
type Collector struct {
	assets  AssetProvider
	dial    Dialer
	samples *sampleStore
	opts    Options
	now     func() time.Time

	mu     sync.RWMutex
	latest map[string]Record
	locks  map[string]*sync.Mutex
	sem    chan struct{}
	stop   chan struct{}
	once   sync.Once
	wg     sync.WaitGroup
}

// New builds a collector. It never dials by itself; call Start for the poller.
func New(assets AssetProvider, dial Dialer, opts Options) (*Collector, error) {
	if assets == nil || dial == nil {
		return nil, errors.New("metrics: asset provider and dialer are required")
	}
	if strings.TrimSpace(opts.DataDir) == "" {
		return nil, errors.New("metrics: data directory is required")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 12 * time.Second
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.History <= 0 {
		opts.History = 288
	}
	st, err := newSampleStore(opts.DataDir, opts.History)
	if err != nil {
		return nil, err
	}
	return &Collector{
		assets: assets, dial: dial, samples: st, opts: opts, now: time.Now,
		latest: map[string]Record{}, locks: map[string]*sync.Mutex{},
		sem: make(chan struct{}, opts.Concurrency), stop: make(chan struct{}),
	}, nil
}

// Start launches the background poller. Interval 0 keeps it dormant, which is
// what the unit tests and single-shot API use.
func (c *Collector) Start() {
	if c.opts.Interval <= 0 {
		return
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.sweep()
		ticker := time.NewTicker(c.opts.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-c.stop:
				return
			case <-ticker.C:
				c.sweep()
			}
		}
	}()
}

// Close stops the poller and waits for in-flight collections.
func (c *Collector) Close() {
	c.once.Do(func() { close(c.stop) })
	c.wg.Wait()
}

// Interval exposes the configured poll period so the UI can label freshness.
func (c *Collector) Interval() time.Duration { return c.opts.Interval }

// Latest returns the cached record for one asset.
func (c *Collector) Latest(assetID string) (Record, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	rec, ok := c.latest[assetID]
	return rec, ok
}

// History returns the stored samples, oldest first.
func (c *Collector) History(assetID string, limit int) []Sample {
	return c.samples.list(assetID, limit)
}

// Collect samples one host right now, ignoring the polling schedule but not the
// per-asset lock: two concurrent requests for the same host still cost one SSH
// round trip.
func (c *Collector) Collect(ctx context.Context, assetID string) Record {
	lock := c.lockFor(assetID)
	lock.Lock()
	defer lock.Unlock()

	started := c.now()
	ctx, cancel := context.WithTimeout(ctx, c.opts.Timeout)
	defer cancel()

	session, err := c.dial.Open(ctx, assetID)
	if err != nil {
		return c.recordFailure(assetID, started, fmt.Errorf("连接失败：%w", err))
	}
	defer session.Close()

	raw, err := session.Run(ctx, Script)
	if err != nil {
		return c.recordFailure(assetID, started, fmt.Errorf("执行采集命令失败：%w", err))
	}
	snap, err := Parse(raw, c.now())
	if err != nil {
		return c.recordFailure(assetID, started, err)
	}
	rec := Record{
		AssetID: assetID, Status: StatusOK, Snapshot: &snap,
		CollectedAt: snap.CollectedAt, DurationMS: c.now().Sub(started).Milliseconds(),
	}
	c.mu.Lock()
	previous := c.latest[assetID]
	c.latest[assetID] = rec
	c.mu.Unlock()
	if previous.Status != StatusOK {
		slog.Info("host metrics recovered", "asset_id", assetID, "summary", snap.Summary())
	}
	if err := c.samples.append(assetID, Sample{T: snap.CollectedAt.Unix(), CPU: snap.CPUPercent, Mem: snap.MemPercent, Disk: snap.DiskPercent, Load1: snap.Load1}); err != nil {
		slog.Warn("persist metrics sample failed", "asset_id", assetID, "error", err)
	}
	return rec
}

// recordFailure stores the error and schedules the next attempt with backoff so
// an unreachable host is not dialed every single tick.
func (c *Collector) recordFailure(assetID string, started time.Time, err error) Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	previous := c.latest[assetID]
	failures := previous.Failures + 1
	backoff := time.Duration(1<<min(failures, 4)) * max(c.opts.Interval, time.Minute)
	rec := Record{
		AssetID: assetID, Status: StatusError, Error: err.Error(),
		CollectedAt: started.UTC(), DurationMS: c.now().Sub(started).Milliseconds(),
		Failures: failures, NextAttempt: c.now().Add(backoff),
	}
	// Keep the last good snapshot visible but clearly marked stale.
	if previous.Snapshot != nil {
		rec.Snapshot = previous.Snapshot
	}
	c.latest[assetID] = rec
	slog.Warn("host metrics failed", "asset_id", assetID, "failures", failures, "error", err.Error())
	return rec
}

func (c *Collector) lockFor(assetID string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	lock := c.locks[assetID]
	if lock == nil {
		lock = &sync.Mutex{}
		c.locks[assetID] = lock
	}
	return lock
}

// sweep collects every eligible host once, respecting per-asset backoff.
func (c *Collector) sweep() {
	ctx, cancel := context.WithTimeout(context.Background(), c.opts.Timeout*2)
	defer cancel()
	assets, err := c.assets.ListAssets(ctx, "")
	if err != nil {
		slog.Warn("metrics sweep skipped", "error", err)
		return
	}
	now := c.now()
	var wg sync.WaitGroup
	for _, a := range assets {
		if a.AssetType != "" && a.AssetType != "linux_host" {
			continue
		}
		// An unverified host key cannot be dialed; skip instead of logging an
		// error every interval.
		if a.HostKeyStatus != "verified" {
			continue
		}
		if rec, ok := c.Latest(a.ID); ok && rec.NextAttempt.After(now) {
			continue
		}
		select {
		case <-c.stop:
			wg.Wait()
			return
		case c.sem <- struct{}{}:
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			defer func() { <-c.sem }()
			c.Collect(context.Background(), id)
		}(a.ID)
	}
	wg.Wait()
}

// runScript executes the collection script in one SSH exec, honouring ctx even
// though the ssh package has no cancellation hook for Output.
func runScript(ctx context.Context, client *ssh.Client, script string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := session.Output("sh -c " + shellQuote(script))
		done <- result{out: string(out), err: err}
	}()
	select {
	case r := <-done:
		return r.out, r.err
	case <-ctx.Done():
		_ = session.Close()
		return "", ctx.Err()
	}
}

// shellQuote wraps s in single quotes for POSIX sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
