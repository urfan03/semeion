package autopilot

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestSuggestMultiMetric(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	for i := 0; i < 90; i++ {
		pts = append(pts, core.DataPoint{
			Time:   start.Add(time.Duration(i) * time.Minute),
			Values: map[string]float64{"cpu": 50, "mem": 60},
		})
	}
	job := Suggest(pts)

	if job.BucketSpan != time.Minute {
		t.Fatalf("bucket span: got %v, want 1m", job.BucketSpan)
	}
	if err := job.Validate(); err != nil {
		t.Fatalf("suggested job invalid: %v", err)
	}

	var means, multivar, count int
	for _, d := range job.Detectors {
		switch {
		case d.IsMultivariate():
			multivar++
		case d.Function == jobspec.FuncMean:
			means++
			if !d.Seasonal {
				t.Fatalf("mean detector should be seasonal (90 points)")
			}
		case d.Function == jobspec.FuncCount:
			count++
		}
	}
	if means != 2 || multivar != 1 || count != 1 {
		t.Fatalf("detectors: means=%d multivar=%d count=%d (want 2/1/1)", means, multivar, count)
	}
}

func TestSuggestSpanHourly(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	for i := 0; i < 48; i++ {
		pts = append(pts, core.DataPoint{Time: start.Add(time.Duration(i) * time.Hour), Value: 10})
	}
	if got := Suggest(pts).BucketSpan; got != time.Hour {
		t.Fatalf("bucket span: got %v, want 1h", got)
	}
}
