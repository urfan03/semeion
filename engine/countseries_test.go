package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestDropSeriesPrunesCountSeries(t *testing.T) {
	job := jobspec.Job{
		Name: "c", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncCount, ByField: "host"}},
	}
	eng, err := New(job)
	if err != nil {
		t.Fatal(err)
	}
	d := job.Detectors[0]

	seed := map[string][]core.DataPoint{"host=a": nil, "host=b": nil}
	eng.zeroFillKnown(d, seed)
	if len(eng.countSeries[d.ID()]) != 2 {
		t.Fatalf("expected 2 tracked count series, got %d", len(eng.countSeries[d.ID()]))
	}

	eng.dropSeries(d.ID() + "|host=a")
	if eng.countSeries[d.ID()]["host=a"] {
		t.Fatalf("an evicted series must be pruned from countSeries")
	}
	if !eng.countSeries[d.ID()]["host=b"] {
		t.Fatalf("a surviving series must stay tracked")
	}

	live := map[string][]core.DataPoint{"host=b": nil}
	eng.zeroFillKnown(d, live)
	if _, ok := live["host=a"]; ok {
		t.Fatalf("an evicted series must not be resurrected by zero-fill")
	}
}
