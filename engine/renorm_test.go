package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestRenormalizeDoesNotFavorProbabilityLessRecords(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	results := []core.BucketResult{
		{Time: t0, Records: []core.Record{{
			Time: t0, Detector: "mean(latency)", Score: 47, Probability: 1e-7,
		}}},
		{Time: t0.Add(time.Minute), Records: []core.Record{{
			Time: t0.Add(time.Minute), Detector: "rare(ua)", Series: "curl/8", Score: 60,

			Probability: probFromScore(60),
		}}},
	}
	RenormalizeResults(results)

	spike := results[0].Records[0].Score
	rare := results[1].Records[0].Score
	if rare >= 100 {
		t.Fatalf("a rare one-off must not pin to 100, got %.1f", rare)
	}

	if spike <= 0 || rare <= 0 {
		t.Fatalf("both records should retain a positive score: spike=%.1f rare=%.1f", spike, rare)
	}

	if spike > 60 {
		t.Fatalf("with a full-scale anchor the 1e-7 spike scores ~58, got %.1f (anchor deflated?)", spike)
	}
}

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
		if i >= 50 && i < 55 {
			v = 1000
		}
		results = append(results, eng.Run([]core.DataPoint{pt(i, v)}, 50)...)
	}

	for _, br := range results {
		if !br.Time.Before(saleStart) && br.Time.Before(saleStart.Add(5*span)) && len(br.Records) > 0 {
			t.Fatalf("calendar window must not emit anomalies, got %+v", br.Records)
		}
	}

	for _, br := range results {
		if br.Time.Before(saleStart.Add(5 * span)) {
			continue
		}
		if len(br.Records) > 0 {
			t.Fatalf("post-window normal value flagged (baseline polluted by the event): %+v", br.Records)
		}
	}
}
