package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestGapFillCountDropToZero(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	for b := 0; b < 60; b++ {
		if b >= 40 && b < 45 {
			continue
		}
		bt := t0.Add(time.Duration(b) * time.Minute)
		for i := 0; i < 100; i++ {
			pts = append(pts, core.DataPoint{Time: bt.Add(time.Duration(i) * 100 * time.Millisecond)})
		}
	}
	job := jobspec.Job{Name: "g", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncCount, Side: jobspec.SideLow}}}
	eng, err := New(job)
	if err != nil {
		t.Fatal(err)
	}
	results := eng.Run(pts, 50)

	flagged := map[time.Time]bool{}
	for _, br := range results {
		for _, r := range br.Records {
			if r.Actual == 0 {
				flagged[r.Time] = true
			}
		}
	}
	if eng.GapsFilled < 5 {
		t.Fatalf("expected >=5 synthesised gap buckets, got %d", eng.GapsFilled)
	}
	for b := 40; b < 45; b++ {
		bt := t0.Add(time.Duration(b) * time.Minute)
		if !flagged[bt] {
			t.Fatalf("silent bucket %d (%v) should be flagged as a zero-count anomaly", b, bt)
		}
	}
}

func TestGapFillOffForMetricJob(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	for b := 0; b < 30; b++ {
		if b >= 10 && b < 20 {
			continue
		}
		bt := t0.Add(time.Duration(b) * time.Minute)
		pts = append(pts, core.DataPoint{Time: bt, Values: map[string]float64{"v": 100}})
	}
	job := jobspec.Job{Name: "m", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", Side: jobspec.SideBoth}}}
	eng, err := New(job)
	if err != nil {
		t.Fatal(err)
	}
	eng.Run(pts, 50)
	if eng.GapsFilled != 0 {
		t.Fatalf("metric-only job must not gap-fill, got %d filled", eng.GapsFilled)
	}
}

func TestGapFillStreaming(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	job := jobspec.Job{Name: "gs", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncCount, Side: jobspec.SideLow}}}
	eng, err := New(job)
	if err != nil {
		t.Fatal(err)
	}
	var results []core.BucketResult
	feed := func(b, n int) {
		bt := t0.Add(time.Duration(b) * time.Minute)
		for i := 0; i < n; i++ {
			results = append(results, eng.Push(core.DataPoint{Time: bt.Add(time.Duration(i) * 100 * time.Millisecond)})...)
		}
	}
	for b := 0; b < 40; b++ {
		feed(b, 100)
	}

	for b := 45; b < 50; b++ {
		feed(b, 100)
	}
	results = append(results, eng.Flush()...)

	flagged := map[time.Time]bool{}
	for _, br := range results {
		for _, r := range br.Records {
			if r.Actual == 0 {
				flagged[r.Time] = true
			}
		}
	}
	for b := 40; b < 45; b++ {
		bt := t0.Add(time.Duration(b) * time.Minute)
		if !flagged[bt] {
			t.Fatalf("streaming silent bucket %d should be flagged as zero-count", b)
		}
	}
}
