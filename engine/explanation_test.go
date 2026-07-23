package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestMultiBucketImpactReported(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	job := jobspec.Job{Name: "mb", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", Side: jobspec.SideHigh}}}
	eng, err := New(job)
	if err != nil {
		t.Fatal(err)
	}
	var pts []core.DataPoint
	for b := 0; b < 70; b++ {
		v := 100.0 + float64((b%5)-2)
		if b >= 45 {
			v = 118
		}
		pts = append(pts, core.DataPoint{Time: t0.Add(time.Duration(b) * time.Minute),
			Value: v, Values: map[string]float64{"v": v}})
	}
	sawMultiBucket := false
	for _, br := range eng.Run(pts, 50) {
		for _, r := range br.Records {
			if r.Kind == "multi_bucket" {
				sawMultiBucket = true
				if r.MultiBucketImpact <= 0 {
					t.Fatalf("a multi_bucket record must carry a positive impact, got %v", r.MultiBucketImpact)
				}
				if r.MultiBucketImpact > 5 {
					t.Fatalf("impact must be capped at 5, got %v", r.MultiBucketImpact)
				}
			}
		}
	}
	if !sawMultiBucket {
		t.Fatal("a sustained modest shift should produce at least one multi_bucket record")
	}
}
