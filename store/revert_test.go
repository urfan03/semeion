package store

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/engine"
)

func TestSnapshotVersioningAndRevert(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStore(dir)

	mk := func(th float64) engine.Snapshot {
		return engine.Snapshot{JobName: "j", Threshold: th, Watermark: time.Unix(int64(th), 0).UTC()}
	}
	v1, err := s.SaveVersion("j", mk(10))
	if err != nil {
		t.Fatal(err)
	}
	v2, err := s.SaveVersion("j", mk(20))
	if err != nil {
		t.Fatal(err)
	}
	if v1 == v2 {
		t.Fatal("version ids must be distinct")
	}

	versions, err := s.ListVersions("j")
	if err != nil || len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %v (err %v)", versions, err)
	}

	live, _, _ := s.Load("j")
	if live.Threshold != 20 {
		t.Fatalf("live snapshot should be the latest (20), got %v", live.Threshold)
	}

	if err := s.Revert("j", v1); err != nil {
		t.Fatal(err)
	}
	live, found, _ := s.Load("j")
	if !found || live.Threshold != 10 {
		t.Fatalf("after revert the live snapshot should be v1 (10), got %v found=%v", live.Threshold, found)
	}

	if err := s.Revert("j", "v999999"); err == nil {
		t.Fatal("reverting to a nonexistent version should error")
	}
}

func TestSnapshotVersionPruning(t *testing.T) {
	s := NewFileStore(t.TempDir())
	for i := 0; i < maxVersions+5; i++ {
		if _, err := s.SaveVersion("j", engine.Snapshot{JobName: "j", Threshold: float64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	versions, _ := s.ListVersions("j")
	if len(versions) != maxVersions {
		t.Fatalf("expected pruning to keep %d versions, got %d", maxVersions, len(versions))
	}
}
