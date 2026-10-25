package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"qiyun-asset-ops/internal/store"
)

// Sample output captured from the real script shape: two /proc/stat reads
// 0.3 s apart, meminfo in kB, df in KiB blocks.
const sampleOutput = `qiyun:host=node-01
qiyun:kernel=5.15.0-119-generic
qiyun:cores=8
qiyun:uptime=864000
qiyun:load=0.34 0.42 0.38
qiyun:os=Ubuntu 24.04.1 LTS
qiyun:cpu1=1000 2000
qiyun:cpu2=1030 2080
qiyun:mem=8192000 4096000 2048000 2097152 2097152
qiyun:fs=/dev/sda2|/|104857600|41943040|40%
qiyun:fs=/dev/sdb1|/data|524288000|104857600|20%
qiyun:end=1
`

func TestParseSnapshot(t *testing.T) {
	now := time.Date(2026, 10, 25, 3, 0, 0, 0, time.UTC)
	snap, err := Parse(sampleOutput, now)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if snap.Hostname != "node-01" || snap.Cores != 8 || snap.Kernel == "" {
		t.Fatalf("host fields wrong: %+v", snap)
	}
	if snap.OS != "Ubuntu 24.04.1 LTS" {
		t.Fatalf("os=%q", snap.OS)
	}
	if snap.UptimeSeconds != 864000 {
		t.Fatalf("uptime=%d", snap.UptimeSeconds)
	}
	if snap.Load1 != 0.34 || snap.Load15 != 0.38 {
		t.Fatalf("load=%v %v", snap.Load1, snap.Load15)
	}
	// idle grew 30 of 80 total ticks => 62.5% busy.
	if snap.CPUPercent != 62.5 {
		t.Fatalf("cpu=%v", snap.CPUPercent)
	}
	// meminfo is kB: total 8192000 kB = 8 GiB, used = total - available = 4 GiB.
	if snap.MemTotalBytes != 8192000*1024 || snap.MemUsedBytes != 4096000*1024 {
		t.Fatalf("mem total=%d used=%d", snap.MemTotalBytes, snap.MemUsedBytes)
	}
	if snap.MemPercent != 50 {
		t.Fatalf("mem percent=%v", snap.MemPercent)
	}
	if len(snap.Disks) != 2 || snap.DiskPercent != 40 {
		t.Fatalf("disks=%+v root=%v", snap.Disks, snap.DiskPercent)
	}
	if !snap.CollectedAt.Equal(now) {
		t.Fatalf("collected_at=%v", snap.CollectedAt)
	}
}

func TestParseRejectsTruncatedPayload(t *testing.T) {
	// A cut-off read can still contain a hostname; without the end marker it
	// must not be presented as a healthy sample.
	truncated := strings.Replace(sampleOutput, "qiyun:end=1\n", "", 1)
	if _, err := Parse(truncated, time.Now()); !errors.Is(err, ErrUnparseable) {
		t.Fatalf("expected ErrUnparseable, got %v", err)
	}
	if _, err := Parse("bash: /proc/stat: No such file\n", time.Now()); !errors.Is(err, ErrUnparseable) {
		t.Fatalf("expected ErrUnparseable, got %v", err)
	}
}

func TestParseToleratesMissingOptionalFields(t *testing.T) {
	// BusyBox / non-GNU hosts often lack nproc or os-release: values must
	// degrade to zero instead of failing the whole sample.
	raw := "qiyun:host=mini\nqiyun:cores=\nqiyun:mem=2048000 1024000 512000 0 0\nqiyun:end=1\n"
	snap, err := Parse(raw, time.Now())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if snap.Cores != 0 || snap.CPUPercent != 0 || snap.MemPercent != 50 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
}

// ---- collector wiring ----

type fakeAssets struct {
	mu    sync.Mutex
	items []store.Asset
	err   error
}

func (f *fakeAssets) ListAssets(context.Context, string) ([]store.Asset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := make([]store.Asset, len(f.items))
	copy(out, f.items)
	return out, nil
}

type fakeDialer struct {
	mu      sync.Mutex
	dials   int
	failFor map[string]error
	output  func() (string, error)
	closed  int
}

func (f *fakeDialer) Open(_ context.Context, assetID string) (Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dials++
	if err := f.failFor[assetID]; err != nil {
		return nil, err
	}
	return &fakeSession{out: f.output, dialer: f}, nil
}

type fakeSession struct {
	out    func() (string, error)
	dialer *fakeDialer
}

func (s *fakeSession) Run(context.Context, string) (string, error) {
	if s.out == nil {
		return sampleOutput, nil
	}
	return s.out()
}

func (s *fakeSession) Close() error {
	s.dialer.mu.Lock()
	s.dialer.closed++
	s.dialer.mu.Unlock()
	return nil
}

func newTestCollector(t *testing.T, assets []store.Asset, dial *fakeDialer, output func() (string, error)) *Collector {
	t.Helper()
	dir := t.TempDir()
	dial.output = output
	c, err := New(&fakeAssets{items: assets}, dial, Options{DataDir: dir, Interval: time.Minute, Timeout: time.Second, History: 3})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return c
}

func verified(id string) store.Asset {
	return store.Asset{ID: id, Name: id, AssetType: "linux_host", HostKeyStatus: "verified"}
}

func TestCollectStoresSnapshotAndHistory(t *testing.T) {
	c := newTestCollector(t, []store.Asset{verified("a1")}, &fakeDialer{}, nil)
	rec := c.Collect(context.Background(), "a1")
	if rec.Status != StatusOK || rec.Snapshot == nil {
		t.Fatalf("record: %+v", rec)
	}
	if rec.DurationMS < 0 {
		t.Fatalf("duration=%d", rec.DurationMS)
	}
	if samples := c.History("a1", 10); len(samples) != 1 || samples[0].CPU != 62.5 {
		t.Fatalf("history: %+v", samples)
	}
	// Ring is capped at Options.History.
	for i := 0; i < 5; i++ {
		c.Collect(context.Background(), "a1")
	}
	if samples := c.History("a1", 10); len(samples) != 3 {
		t.Fatalf("history not capped: %d", len(samples))
	}
}

func TestCollectRecordsFailureAndKeepsLastSnapshot(t *testing.T) {
	dial := &fakeDialer{}
	c := newTestCollector(t, []store.Asset{verified("a1")}, dial, nil)
	if rec := c.Collect(context.Background(), "a1"); rec.Status != StatusOK {
		t.Fatalf("first collect: %+v", rec)
	}
	dial.failFor = map[string]error{"a1": errors.New("dial tcp 10.0.0.9:22: i/o timeout")}
	rec := c.Collect(context.Background(), "a1")
	if rec.Status != StatusError || !strings.Contains(rec.Error, "连接失败") {
		t.Fatalf("record: %+v", rec)
	}
	if rec.Snapshot == nil {
		// The last good sample stays visible but the record is marked error.
		t.Fatal("expected stale snapshot preserved")
	}
	if !rec.NextAttempt.After(time.Now()) {
		t.Fatalf("expected backoff scheduled, got %v", rec.NextAttempt)
	}
}

func TestHistoryPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	dial := &fakeDialer{}
	c, err := New(&fakeAssets{items: []store.Asset{verified("a1")}}, dial, Options{DataDir: dir, History: 5})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	dial.output = func() (string, error) { return sampleOutput, nil }
	c.Collect(context.Background(), "a1")

	again, err := New(&fakeAssets{}, dial, Options{DataDir: dir, History: 5})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if samples := again.History("a1", 10); len(samples) != 1 {
		t.Fatalf("history lost after restart: %+v", samples)
	}
	if _, err := os.Stat(filepath.Join(dir, "metrics", "a1.json")); err != nil {
		t.Fatalf("sample file missing: %v", err)
	}
}

func TestSweepSkipsUnverifiedAndHonoursBackoff(t *testing.T) {
	assets := []store.Asset{
		verified("a1"),
		{ID: "a2", AssetType: "linux_host", HostKeyStatus: "pending"},
		{ID: "a3", AssetType: "network_device", HostKeyStatus: "verified"},
	}
	dial := &fakeDialer{failFor: map[string]error{"a1": errors.New("boom")}}
	c := newTestCollector(t, assets, dial, nil)
	c.sweep()
	if dial.dials != 1 {
		t.Fatalf("only the verified linux host should be dialed, got %d", dial.dials)
	}
	if rec, _ := c.Latest("a2"); rec.Status != "" {
		t.Fatalf("unverified asset should have no record, got %+v", rec)
	}
	// Second sweep inside the backoff window must not dial again.
	c.sweep()
	if dial.dials != 1 {
		t.Fatalf("backoff ignored, dials=%d", dial.dials)
	}
}

// ---- HTTP ----

func serve(t *testing.T, c *Collector) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	NewHandler(c).Register(mux, func(next http.Handler) http.Handler { return next })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func getJSON(t *testing.T, url string, out any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
	return resp.StatusCode
}

func TestHTTPListIncludesPendingHosts(t *testing.T) {
	c := newTestCollector(t, []store.Asset{verified("a1"), verified("a2")}, &fakeDialer{}, nil)
	c.Collect(context.Background(), "a1")
	srv := serve(t, c)

	var body struct {
		IntervalSeconds int    `json:"interval_seconds"`
		Items           []item `json:"items"`
	}
	if code := getJSON(t, srv.URL+"/api/v1/metrics", &body); code != 200 {
		t.Fatalf("status=%d", code)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items=%d", len(body.Items))
	}
	byID := map[string]item{}
	for _, it := range body.Items {
		byID[it.AssetID] = it
	}
	if byID["a1"].Status != StatusOK || byID["a1"].Snapshot == nil {
		t.Fatalf("a1=%+v", byID["a1"])
	}
	if byID["a2"].Status != StatusPending {
		t.Fatalf("a2 should be pending, got %+v", byID["a2"])
	}
	if body.IntervalSeconds != 60 {
		t.Fatalf("interval=%d", body.IntervalSeconds)
	}
}

func TestHTTPCollectAndHistory(t *testing.T) {
	c := newTestCollector(t, []store.Asset{verified("a1")}, &fakeDialer{}, nil)
	srv := serve(t, c)

	resp, err := http.Post(srv.URL+"/api/v1/metrics/a1/collect", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	var got item
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if got.Status != StatusOK || got.Snapshot == nil || got.Snapshot.CPUPercent != 62.5 {
		t.Fatalf("collect payload: %+v", got)
	}

	var hist struct {
		Samples []Sample `json:"samples"`
	}
	if code := getJSON(t, srv.URL+"/api/v1/metrics/a1/history?limit=5", &hist); code != 200 {
		t.Fatalf("history status=%d", code)
	}
	if len(hist.Samples) != 1 || hist.Samples[0].Mem != 50 {
		t.Fatalf("history payload: %+v", hist.Samples)
	}
}

func TestHTTPCollectReportsRemoteFailureAsHostError(t *testing.T) {
	dial := &fakeDialer{failFor: map[string]error{"a1": errors.New("dial tcp 10.0.0.9:22: i/o timeout")}}
	c := newTestCollector(t, []store.Asset{verified("a1")}, dial, nil)
	srv := serve(t, c)
	resp, err := http.Post(srv.URL+"/api/v1/metrics/a1/collect", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	// The request succeeded; the host did not. That is not a 502.
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var got item
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != StatusError || !strings.Contains(got.Error, "i/o timeout") {
		t.Fatalf("payload: %+v", got)
	}
}

func TestHTTPUnknownAssetIs404(t *testing.T) {
	c := newTestCollector(t, nil, &fakeDialer{}, nil)
	srv := serve(t, c)
	if code := getJSON(t, srv.URL+"/api/v1/metrics/ghost/history", nil); code != 404 {
		t.Fatalf("status=%d", code)
	}
	resp, err := http.Post(srv.URL+"/api/v1/metrics/ghost/collect", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("collect status=%d", resp.StatusCode)
	}
}
