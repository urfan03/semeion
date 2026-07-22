// Package store persists engine snapshots so a long-running detector keeps its
// learned baselines across restarts. Two implementations ship: an in-memory
// store (tests) and a file store (single-binary default). External backends
// (Redis, Postgres, BoltDB) can implement the same interface later.
package store

import (
	"encoding/json"
	"os"
	"path/filepath"

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
	b, err := os.ReadFile(s.path(name))
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
