package benchmark

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/engine"
	"github.com/urfan03/semeion/jobspec"
)

// On a stationary series with clearly injected spikes, the robust detector must
// catch every anomaly (recall 1.0) with few false positives (precision high).
func TestEngineQualityOnSpikes(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	span := time.Minute
	lbl := Generate(start, span, 240, []int{80, 140, 200}, 3.0)

	job := jobspec.Job{
		Name:       "bench",
		BucketSpan: span,
		Detectors:  []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v"}},
	}
	eng, err := engine.New(job)
	if err != nil {
		t.Fatal(err)
	}
	results := eng.Run(lbl.Points, 50)

	res := Score(results, lbl.Anomalies, span, 1)
	if res.Recall < 1.0 {
		t.Fatalf("recall: got %.2f, want 1.0 (TP=%d FN=%d)", res.Recall, res.TP, res.FN)
	}
	if res.Precision < 0.5 {
		t.Fatalf("precision: got %.2f, want >= 0.5 (TP=%d FP=%d)", res.Precision, res.TP, res.FP)
	}
}
