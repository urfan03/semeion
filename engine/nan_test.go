package engine

import (
	"math"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestNonFiniteInputDoesNotPoisonModel(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	for b := 0; b < 60; b++ {
		bt := t0.Add(time.Duration(b) * time.Minute)
		v := 100.0
		if b == 20 {
			v = math.NaN()
		}
		if b == 21 {
			v = math.Inf(1)
		}
		if b == 50 {
			v = 900
		}
		pts = append(pts, core.DataPoint{Time: bt, Value: v, Values: map[string]float64{"v": v}})
	}
	job := jobspec.Job{Name: "n", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", Side: jobspec.SideHigh}}}
	eng, err := New(job)
	if err != nil {
		t.Fatal(err)
	}
	flaggedSpike := false
	for _, br := range eng.Run(pts, 50) {
		for _, r := range br.Records {
			if math.IsNaN(r.Score) || math.IsInf(r.Score, 0) {
				t.Fatalf("a non-finite input produced a non-finite score at %v", r.Time)
			}
			if br.Time.Equal(t0.Add(50 * time.Minute)) {
				flaggedSpike = true
			}
		}
	}
	if !flaggedSpike {
		t.Fatal("the real spike at bucket 50 should still be detected after NaN/Inf inputs")
	}
}
