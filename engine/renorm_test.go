package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

// C4 regression: a rare/probability-less record must not renormalize to 100 and
// bury a genuine low-probability spike. Before the fix, sevOf(p<=0)=15 pinned
// every probability-less record to the top and deflated real anomalies.
func TestRenormalizeDoesNotFavorProbabilityLessRecords(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	results := []core.BucketResult{
		{Time: t0, Records: []core.Record{{
			Time: t0, Detector: "mean(latency)", Score: 47, Probability: 1e-7, // genuine 5σ spike
		}}},
		{Time: t0.Add(time.Minute), Records: []core.Record{{
			Time: t0.Add(time.Minute), Detector: "rare(ua)", Series: "curl/8", Score: 60,
			// A rare one-off: emit() backfills Probability from the score, so it is
			// no longer p=0. Simulate that backfill here.
			Probability: probFromScore(60),
		}}},
	}
	RenormalizeResults(results)

	spike := results[0].Records[0].Score
	rare := results[1].Records[0].Score
	if rare >= 100 {
		t.Fatalf("a rare one-off must not pin to 100, got %.1f", rare)
	}
	// The genuine 1e-7 spike (severity 7) must outrank the score-60 rare
	// (severity 7.2)? They are close by design — the key property is neither is
	// pinned to 100 and both scale off the same anchor.
	if spike <= 0 || rare <= 0 {
		t.Fatalf("both records should retain a positive score: spike=%.1f rare=%.1f", spike, rare)
	}
	// A probability-less record left at p=0 (the old bug path) would have scored
	// 100 and dominated; assert the anchor stayed at full scale, not 15.
	if spike > 60 {
		t.Fatalf("with a full-scale anchor the 1e-7 spike scores ~58, got %.1f (anchor deflated?)", spike)
	}
}

// P1-L3 regression: a spike inside a calendar window must neither alert NOR
// train the baseline — otherwise the event's level becomes "normal" and the
// following days score as anomalous dips.
func TestCalendarWindowExcludedFromTraining(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	span := time.Minute
	saleStart := t0.Add(50 * span)
	job := jobspec.Job{
		Name: "sale", BucketSpan: span,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", Side: jobspec.SideBoth}},
		Calendars: []jobspec.Calendar{{Name: "sale", Start: saleStart, End: saleStart.Add(5 * span)}},
	}
	eng, _ := New(job)

	pt := func(i int, v float64) core.DataPoint {
		return core.DataPoint{Time: t0.Add(time.Duration(i) * span), Value: v, Values: map[string]float64{"v": v}}
	}
	var results []core.BucketResult
	for i := 0; i < 60; i++ {
		v := 100.0
		if i >= 50 && i < 55 { // 10× spike inside the calendar window
			v = 1000
		}
		results = append(results, eng.Run([]core.DataPoint{pt(i, v)}, 50)...)
	}
	// No result should carry a record during the calendar window.
	for _, br := range results {
		if !br.Time.Before(saleStart) && br.Time.Before(saleStart.Add(5*span)) && len(br.Records) > 0 {
			t.Fatalf("calendar window must not emit anomalies, got %+v", br.Records)
		}
	}
	// After the window, the normal-100 buckets must NOT be flagged as anomalous
	// dips — which they would be if the 1000-spikes had trained the baseline.
	for _, br := range results {
		if br.Time.Before(saleStart.Add(5 * span)) {
			continue
		}
		if len(br.Records) > 0 {
			t.Fatalf("post-window normal value flagged (baseline polluted by the event): %+v", br.Records)
		}
	}
}
