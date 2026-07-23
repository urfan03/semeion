package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestRatioDetectsErrorRateSpike(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	for b := 0; b < 60; b++ {
		bt := t0.Add(time.Duration(b) * time.Minute)
		errs := 2.0
		total := 1000.0
		if b == 50 {
			errs = 400
		}
		pts = append(pts, core.DataPoint{Time: bt, Values: map[string]float64{"errors": errs, "total": total}})
	}
	job := jobspec.Job{Name: "r", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncRatio, Field: "errors", DenomField: "total", Side: jobspec.SideHigh}}}
	eng, err := New(job)
	if err != nil {
		t.Fatal(err)
	}
	var hit *core.Record
	for _, br := range eng.Run(pts, 50) {
		if !br.Time.Equal(t0.Add(50 * time.Minute)) {
			continue
		}
		for i := range br.Records {
			hit = &br.Records[i]
		}
	}
	if hit == nil {
		t.Fatal("a 0.4 error ratio against a ~0.002 baseline should be flagged")
	}
	if hit.Kind != "ratio" {
		t.Fatalf("kind should be ratio, got %q", hit.Kind)
	}
	if hit.Actual < 0.39 || hit.Actual > 0.41 {
		t.Fatalf("actual ratio should be ~0.4, got %v", hit.Actual)
	}
}

func TestRatioZeroDenominatorSkipped(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	job := jobspec.Job{Name: "r", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncRatio, Field: "errors", DenomField: "total", Side: jobspec.SideHigh}}}
	eng, _ := New(job)
	pts := []core.DataPoint{{Time: t0, Values: map[string]float64{"errors": 5, "total": 0}}}
	for _, br := range eng.Run(pts, 50) {
		if len(br.Records) != 0 {
			t.Fatal("a zero denominator must not produce a record")
		}
	}
}
