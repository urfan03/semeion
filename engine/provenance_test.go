package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestInitialScorePreservedAcrossRenormalize(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	results := []core.BucketResult{
		{Time: t0, Score: 88, Records: []core.Record{
			{Time: t0, Detector: "mean(v)", Series: "a", Score: 88, Probability: 1e-9},
		}},
	}
	RenormalizeResults(results)
	r := results[0].Records[0]
	if r.InitialScore != 88 {
		t.Fatalf("initial_score should preserve the pre-renormalization score 88, got %v", r.InitialScore)
	}
	if results[0].InitialScore != 88 {
		t.Fatalf("bucket initial_score should be preserved, got %v", results[0].InitialScore)
	}
}

func TestMemoryStatus(t *testing.T) {
	job := jobspec.Job{Name: "m", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", ByField: "h", Side: jobspec.SideHigh}}}
	eng, _ := New(job)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	for b := 0; b < 40; b++ {
		for _, h := range []string{"a", "b", "c"} {
			pts = append(pts, core.DataPoint{Time: t0.Add(time.Duration(b) * time.Minute),
				Value: 100, Fields: map[string]string{"h": h}, Values: map[string]float64{"v": 100}})
		}
	}
	eng.Run(pts, 50)
	b, status := eng.MemoryStatus()
	if b <= 0 || status != "ok" {
		t.Fatalf("expected positive bytes and ok status with no limit, got %d %q", b, status)
	}
	eng.ModelMemoryLimit = b / 2
	if _, status := eng.MemoryStatus(); status != "hard_limit" {
		t.Fatalf("expected hard_limit when usage exceeds the limit, got %q", status)
	}
}
