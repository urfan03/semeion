package engine

import (
	"math"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestScoreCalibrationLowFalsePositiveRate(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seed := uint64(88172645463325252)
	norm := func() float64 {
		u := func() float64 {
			seed ^= seed << 13
			seed ^= seed >> 7
			seed ^= seed << 17
			return float64(seed%1000000)/1000000 + 1e-9
		}
		return math.Sqrt(-2*math.Log(u())) * math.Cos(2*math.Pi*u())
	}
	n := 12000
	pts := make([]core.DataPoint, 0, n)
	for i := 0; i < n; i++ {
		v := 100 + 15*norm()
		pts = append(pts, core.DataPoint{Time: t0.Add(time.Duration(i) * time.Minute), Value: v,
			Values: map[string]float64{"v": v}})
	}
	job := jobspec.Job{Name: "cal", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", Side: jobspec.SideBoth}}}
	eng, err := New(job)
	if err != nil {
		t.Fatal(err)
	}
	flagged, total := 0, 0
	for _, br := range eng.Run(pts, 50) {
		total++
		if br.Score >= 50 {
			flagged++
		}
	}
	rate := float64(flagged) / float64(total)
	if rate > 0.005 {
		t.Fatalf("false-positive rate on stationary noise should be < 0.5%%, got %.3f%% (%d/%d)", rate*100, flagged, total)
	}
}
