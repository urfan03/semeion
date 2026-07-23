package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

// #2: on a chronically noisy series, adaptive sensitivity suppresses routine
// above-threshold bumps (which a fixed threshold would over-report), while a
// non-adaptive job reports them all.
func TestAdaptiveSensitivitySuppressesRoutineNoise(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// A series that jitters enough to frequently clear a fixed threshold of 50.
	mkPts := func() []core.DataPoint {
		var pts []core.DataPoint
		for i := 0; i < 400; i++ {
			// Flat baseline of 100, with a recurring bump of VARYING size every 5th
			// bucket → the median stays 100 (bumps are outliers) and the bump scores
			// spread, so the per-series quantile can tell the routine from the rare.
			v := 100.0
			if i%5 == 0 {
				v = 100 + float64((i/5)%10+2)*20 // bumps 40,60,…,220 cycling
			}
			pts = append(pts, core.DataPoint{Time: t0.Add(time.Duration(i) * time.Minute),
				Value: v, Values: map[string]float64{"v": v}})
		}
		return pts
	}

	base := jobspec.Job{Name: "n", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", Side: jobspec.SideHigh}}}
	adaptive := base
	adaptive.Sensitivity = 0.98

	count := func(job jobspec.Job) int {
		eng, _ := New(job)
		n := 0
		for _, br := range eng.Run(mkPts(), 50) {
			n += len(br.Records)
		}
		return n
	}

	fixed := count(base)
	gated := count(adaptive)
	if gated >= fixed {
		t.Fatalf("adaptive sensitivity should report fewer than the fixed threshold: fixed=%d gated=%d", fixed, gated)
	}
	if fixed == 0 {
		t.Fatal("test fixture produced no anomalies to gate")
	}
}
