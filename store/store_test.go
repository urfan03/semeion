package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/engine"
	"github.com/urfan03/semeion/jobspec"
)

func job() jobspec.Job {
	return jobspec.Job{
		Name:       "persist-test",
		BucketSpan: time.Minute,
		Detectors:  []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v"}},
	}
}

func warmEngine(t *testing.T) *engine.Engine {
	t.Helper()
	e, err := engine.New(job())
	if err != nil {
		t.Fatal(err)
	}
	e.SetThreshold(50)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 40; i++ {
		e.Push(core.DataPoint{Time: start.Add(time.Duration(i) * time.Minute), Value: 100})
	}
	return e
}

func TestFileStoreRoundTrip(t *testing.T) {
	e := warmEngine(t)
	snap := e.Snapshot()

	st := NewFileStore(filepath.Join(t.TempDir(), "state"))
	if err := st.Save("persist-test", snap); err != nil {
		t.Fatal(err)
	}
	got, found, err := st.Load("persist-test")
	if err != nil || !found {
		t.Fatalf("load: found=%v err=%v", found, err)
	}
	if len(got.Models) != len(snap.Models) || len(got.Models) == 0 {
		t.Fatalf("models: got %d want %d", len(got.Models), len(snap.Models))
	}
	if !got.Watermark.Equal(snap.Watermark) {
		t.Fatalf("watermark: got %v want %v", got.Watermark, snap.Watermark)
	}
}

func TestFileStoreMissing(t *testing.T) {
	st := NewFileStore(t.TempDir())
	if _, found, err := st.Load("nope"); err != nil || found {
		t.Fatalf("expected not-found, no error: found=%v err=%v", found, err)
	}
}

func TestMemStore(t *testing.T) {
	st := NewMemStore()
	if err := st.Save("k", warmEngine(t).Snapshot()); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := st.Load("k"); !found {
		t.Fatal("expected found")
	}
}
