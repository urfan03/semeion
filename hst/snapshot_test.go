package hst

import (
	"math/rand/v2"
	"testing"
)

func TestSnapshotResumesStream(t *testing.T) {
	opt := Options{Trees: 12, Height: 6, WindowSize: 64, Seed: 77}
	rng := rand.New(rand.NewPCG(2, 3))
	feed := make([][]float64, 600)
	for i := range feed {
		feed[i] = []float64{rng.Float64(), rng.Float64()}
	}

	straight := New(2, opt)
	want := make([]float64, len(feed))
	for i, x := range feed {
		want[i] = straight.Update(x)
	}

	split := New(2, opt)
	got := make([]float64, len(feed))
	for i := 0; i < 300; i++ {
		got[i] = split.Update(feed[i])
	}
	blob, err := split.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Restore(blob)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Dims() != 2 || resumed.Warm() != split.Warm() {
		t.Fatalf("restored forest has the wrong shape: dims=%d warm=%v", resumed.Dims(), resumed.Warm())
	}
	for i := 300; i < len(feed); i++ {
		got[i] = resumed.Update(feed[i])
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("a snapshot/restore must not change the stream, diverged at %d: %v vs %v", i, want[i], got[i])
		}
	}
}

func TestRestoreRejectsBadSnapshots(t *testing.T) {
	if _, err := Restore([]byte("not json")); err == nil {
		t.Fatal("malformed JSON must error")
	}
	if _, err := Restore([]byte(`{"version":2}`)); err == nil {
		t.Fatal("an unknown version must error")
	}
	if _, err := Restore([]byte(`{"version":1,"dims":2,"options":{"Trees":4,"Height":3,"WindowSize":8},"nodes":[]}`)); err == nil {
		t.Fatal("a truncated node list must error")
	}
}

func TestSeriesMultiScoresJointOutliers(t *testing.T) {
	rng := rand.New(rand.NewPCG(31, 37))
	n := 2000
	rows := make([][]float64, n)
	for i := range rows {
		x := rng.Float64()
		rows[i] = []float64{x, x + 0.01*rng.NormFloat64()}
	}
	at := 1700
	rows[at] = []float64{0.1, 0.9}

	scores := SeriesMulti(rows, Options{Trees: 30, Height: 8, WindowSize: 250, Seed: 5})
	if len(scores) != n {
		t.Fatalf("expected %d scores, got %d", n, len(scores))
	}
	var elsewhere float64
	cnt := 0
	for i, s := range scores {
		if i == at || i < 500 {
			continue
		}
		elsewhere += s
		cnt++
	}
	if scores[at] <= elsewhere/float64(cnt) {
		t.Fatalf("breaking the correlation must score above the baseline: %.4f vs %.4f", scores[at], elsewhere/float64(cnt))
	}
	if SeriesMulti(nil, Options{}) != nil {
		t.Fatal("no rows must give no scores")
	}
}
