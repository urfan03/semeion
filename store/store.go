// Package store persists engine snapshots so a long-running detector keeps its
// learned baselines across restarts. Two implementations ship: an in-memory
// store (tests) and a file store (single-binary default). External backends
// (Redis, Postgres, BoltDB) can implement the same interface later.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/urfan03/semeion/engine"
)

// Store saves and loads engine snapshots by job name.
type Store interface {
	Save(name string, snap engine.Snapshot) error
	Load(name string) (snap engine.Snapshot, found bool, err error)
}

// MemStore keeps snapshots in memory (not durable; for tests / ephemeral runs).
type MemStore struct{ m map[string]engine.Snapshot }

func NewMemStore() *MemStore { return &MemStore{m: make(map[string]engine.Snapshot)} }

func (s *MemStore) Save(name string, snap engine.Snapshot) error {
	s.m[name] = snap
	return nil
}

func (s *MemStore) Load(name string) (engine.Snapshot, bool, error) {
	snap, ok := s.m[name]
	return snap, ok, nil
}

// FileStore writes one JSON file per job under Dir.
type FileStore struct{ Dir string }

func NewFileStore(dir string) *FileStore { return &FileStore{Dir: dir} }

func (s *FileStore) path(name string) string {
	return filepath.Join(s.Dir, name+".json")
}

func (s *FileStore) Save(name string, snap engine.Snapshot) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	// Write to a temp file then rename, so a crash/SIGKILL mid-write can't leave
	// a truncated JSON that fails to load and blocks the next start.
	path := s.path(name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *FileStore) Load(name string) (engine.Snapshot, bool, error) {
	return loadSnapshotFile(s.path(name))
}

func loadSnapshotFile(path string) (engine.Snapshot, bool, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return engine.Snapshot{}, false, nil
	}
	if err != nil {
		return engine.Snapshot{}, false, err
	}
	var snap engine.Snapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return engine.Snapshot{}, false, err
	}
	return snap, true, nil
}

// ── Versioned snapshots (revert) ─────────────────────────────────────────────
//
// Elastic ML keeps a history of model snapshots so an operator can REVERT after
// a bad training window (a big one-off event poisoned the baseline). SaveVersion
// keeps the last maxVersions timestamp-ordered copies alongside the live file;
// Revert promotes a chosen one back to the live file so the next Load uses it.

// maxVersions bounds how many historical snapshots are retained per job.
const maxVersions = 20

func (s *FileStore) versionsDir(name string) string {
	return filepath.Join(s.Dir, name+".versions")
}

// SaveVersion writes the snapshot as the live file AND appends an immutable,
// zero-padded, sequentially-numbered version under <name>.versions/, pruning the
// oldest beyond maxVersions. Returns the new version id. The id is derived from
// the existing count (not wall-clock), so it stays deterministic.
func (s *FileStore) SaveVersion(name string, snap engine.Snapshot) (string, error) {
	if err := s.Save(name, snap); err != nil {
		return "", err
	}
	dir := s.versionsDir(name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	existing, _ := s.ListVersions(name)
	next := len(existing) + 1
	// Ensure monotonic id even after pruning: base it on the highest seen index.
	for _, v := range existing {
		var n int
		if _, err := fmt.Sscanf(v, "v%d", &n); err == nil && n >= next {
			next = n + 1
		}
	}
	version := fmt.Sprintf("v%06d", next)
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, version+".json"), b, 0o644); err != nil {
		return "", err
	}
	s.pruneVersions(name)
	return version, nil
}

// ListVersions returns the retained version ids for a job, oldest first.
func (s *FileStore) ListVersions(name string) ([]string, error) {
	entries, err := os.ReadDir(s.versionsDir(name))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if filepath.Ext(n) == ".json" {
			out = append(out, n[:len(n)-len(".json")])
		}
	}
	sort.Strings(out)
	return out, nil
}

// LoadVersion loads a specific historical snapshot.
func (s *FileStore) LoadVersion(name, version string) (engine.Snapshot, bool, error) {
	return loadSnapshotFile(filepath.Join(s.versionsDir(name), version+".json"))
}

// Revert promotes a historical version back to the live file, so the next Load
// (and thus the next run/restart) resumes from that snapshot.
func (s *FileStore) Revert(name, version string) error {
	snap, found, err := s.LoadVersion(name, version)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("snapshot %s/%s not found", name, version)
	}
	return s.Save(name, snap)
}

// pruneVersions deletes the oldest versions beyond maxVersions.
func (s *FileStore) pruneVersions(name string) {
	versions, _ := s.ListVersions(name)
	if len(versions) <= maxVersions {
		return
	}
	for _, v := range versions[:len(versions)-maxVersions] {
		_ = os.Remove(filepath.Join(s.versionsDir(name), v+".json"))
	}
}
