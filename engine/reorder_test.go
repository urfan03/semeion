package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

// C8 regression: a late point whose bucket already closed must be dropped, not
// re-open the bucket into a duplicate, out-of-order result.
func TestStreamingDropsLateClosedBucket(t *testing.T) {
	job := jobspec.Job{Name: "s", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v"}}}
	eng, _ := New(job)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	pt := func(min int, v float64) core.DataPoint {
		return core.DataPoint{Time: t0.Add(time.Duration(min) * time.Minute), Value: v,
			Values: map[string]float64{"v": v}}
	}

	var all []core.BucketResult
	all = append(all, eng.Push(pt(0, 100))...)
	all = append(all, eng.Push(pt(1, 100))...) // closes bucket 0
	all = append(all, eng.Push(pt(2, 100))...) // closes bucket 1
	// A late point for bucket 0, which already closed:
	late := eng.Push(pt(0, 900))
	if late != nil {
		t.Fatalf("a late point for a closed bucket must return no result, got %+v", late)
	}
	if eng.LateDropped != 1 {
		t.Fatalf("expected 1 late-dropped point, got %d", eng.LateDropped)
	}
	all = append(all, eng.Flush()...)

	// No bucket time should appear twice in the emitted results.
	seen := map[time.Time]int{}
	for _, br := range all {
		seen[br.Time]++
	}
	for bt, n := range seen {
		if n > 1 {
			t.Fatalf("bucket %s emitted %d times (duplicate from reordering)", bt, n)
		}
	}
}

// A late point whose bucket is still OPEN (not yet closed) is still folded in.
func TestStreamingAcceptsLatePointInOpenBucket(t *testing.T) {
	job := jobspec.Job{Name: "s", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v"}}}
	eng, _ := New(job)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pt := func(sec int, v float64) core.DataPoint {
		return core.DataPoint{Time: t0.Add(time.Duration(sec) * time.Second), Value: v,
			Values: map[string]float64{"v": v}}
	}
	// Two points in the same (still-open) bucket 0, arriving "out of order" within it.
	eng.Push(pt(30, 100))
	eng.Push(pt(10, 100)) // earlier timestamp, same bucket, bucket still open
	if eng.LateDropped != 0 {
		t.Fatalf("a point in the still-open bucket must not be dropped, got %d dropped", eng.LateDropped)
	}
}
