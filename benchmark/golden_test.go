package benchmark

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/engine"
	"github.com/urfan03/semeion/jobspec"
)

func TestGoldenPerfectDetectionOnClearSpikes(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	span := time.Minute
	spikes := []int{80, 140, 200}
	lbl := Generate(start, span, 240, spikes, 4.0)

	job := jobspec.Job{Name: "golden", BucketSpan: span,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", Side: jobspec.SideHigh}}}
	eng, err := engine.New(job)
	if err != nil {
		t.Fatal(err)
	}
	results := eng.Run(lbl.Points, 50)

	res := Score(results, lbl.Anomalies, span, 1)
	if res.TP != len(spikes) || res.FP != 0 || res.FN != 0 {
		t.Fatalf("golden: expected exact detection of %d spikes with no FP/FN, got TP=%d FP=%d FN=%d (F1=%.3f)",
			len(spikes), res.TP, res.FP, res.FN, res.F1)
	}
	if res.F1 != 1.0 {
		t.Fatalf("golden: F1 must be 1.0 on clear well-separated spikes, got %.3f", res.F1)
	}
}
