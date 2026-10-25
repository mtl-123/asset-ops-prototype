package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

// Sample is the compact history point used for the trend line. Keeping this
// small matters: 288 points × 2 KB would be pointless payload on every poll.
type Sample struct {
	T     int64   `json:"t"`
	CPU   float64 `json:"cpu"`
	Mem   float64 `json:"mem"`
	Disk  float64 `json:"disk"`
	Load1 float64 `json:"load1"`
}

type sampleFile struct {
	Samples []Sample `json:"samples"`
}

// sampleStore keeps a bounded ring of samples per asset, in memory and on disk.
// JSON files (not SQLite) keep the metrics package independent of the store
// schema; a corrupt file only loses history, never asset records.
type sampleStore struct {
	mu      sync.Mutex
	dir     string
	limit   int
	byAsset map[string][]Sample
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func newSampleStore(dataDir string, limit int) (*sampleStore, error) {
	dir := filepath.Join(dataDir, "metrics")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &sampleStore{dir: dir, limit: limit, byAsset: map[string][]Sample{}}
	s.load()
	return s, nil
}

func (s *sampleStore) load() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-len(".json")]
		if !safeID.MatchString(id) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			continue
		}
		var file sampleFile
		if json.Unmarshal(raw, &file) != nil {
			continue
		}
		if len(file.Samples) > s.limit {
			file.Samples = file.Samples[len(file.Samples)-s.limit:]
		}
		s.byAsset[id] = file.Samples
	}
}

func (s *sampleStore) append(assetID string, sample Sample) error {
	if !safeID.MatchString(assetID) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	samples := append(s.byAsset[assetID], sample)
	if len(samples) > s.limit {
		samples = samples[len(samples)-s.limit:]
	}
	s.byAsset[assetID] = samples
	return s.flush(assetID, samples)
}

func (s *sampleStore) list(assetID string, limit int) []Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	samples := s.byAsset[assetID]
	if limit > 0 && len(samples) > limit {
		samples = samples[len(samples)-limit:]
	}
	out := make([]Sample, len(samples))
	copy(out, samples)
	return out
}

// flush writes through a temp file + rename so a crash mid-write cannot leave a
// half file that fails to parse on the next boot.
func (s *sampleStore) flush(assetID string, samples []Sample) error {
	raw, err := json.Marshal(sampleFile{Samples: samples})
	if err != nil {
		return err
	}
	target := filepath.Join(s.dir, assetID+".json")
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}
