package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/urfan03/semeion/engine"
)

type Store interface {
	Save(name string, snap engine.Snapshot) error
	Load(name string) (snap engine.Snapshot, found bool, err error)
}

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

const maxVersions = 20

func (s *FileStore) versionsDir(name string) string {
	return filepath.Join(s.Dir, name+".versions")
}

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

func (s *FileStore) LoadVersion(name, version string) (engine.Snapshot, bool, error) {
	return loadSnapshotFile(filepath.Join(s.versionsDir(name), version+".json"))
}

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

func (s *FileStore) pruneVersions(name string) {
	versions, _ := s.ListVersions(name)
	if len(versions) <= maxVersions {
		return
	}
	for _, v := range versions[:len(versions)-maxVersions] {
		_ = os.Remove(filepath.Join(s.versionsDir(name), v+".json"))
	}
}

func (s *FileStore) RetainVersions(name string, maxAge time.Duration) (removed int, err error) {
	if maxAge <= 0 {
		return 0, nil
	}
	versions, err := s.ListVersions(name)
	if err != nil {
		return 0, err
	}
	dir := s.versionsDir(name)
	cutoff := time.Now().Add(-maxAge)
	for i, v := range versions {
		if i == len(versions)-1 {
			break
		}
		p := filepath.Join(dir, v+".json")
		fi, statErr := os.Stat(p)
		if statErr != nil {
			continue
		}
		if fi.ModTime().Before(cutoff) {
			if os.Remove(p) == nil {
				removed++
			}
		}
	}
	return removed, nil
}
