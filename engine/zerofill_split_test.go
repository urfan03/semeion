package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestPerPartitionCountZeroFill(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	for b := 0; b < 60; b++ {
		bt := t0.Add(time.Duration(b) * time.Minute)
		for i := 0; i < 40; i++ {
			pts = append(pts, core.DataPoint{Time: bt, Fields: map[string]string{"host": "a"}})
		}
		if b < 30 { // host b sends traffic, then goes completely silent from bucket 30
			for i := 0; i < 40; i++ {
				pts = append(pts, core.DataPoint{Time: bt, Fields: map[string]string{"host": "b"}})
			}
		}
	}
	job := jobspec.Job{Name: "z", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncCount, ByField: "host", Side: jobspec.SideLow}}}
	eng, err := New(job)
	if err != nil {
		t.Fatal(err)
	}
	silentBFlagged := false
	for _, br := range eng.Run(pts, 50) {
		for _, r := range br.Records {
			if r.Series == "host=b" && r.Actual == 0 {
				silentBFlagged = true
			}
		}
	}
	if !silentBFlagged {
		t.Fatal("host=b dropping to zero traffic should be flagged as a zero-count anomaly (per-partition zero-fill)")
	}
}
