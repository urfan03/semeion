package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/detect"
	"github.com/urfan03/semeion/jobspec"
)

func TestRateDetectsBurst(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	for b := 0; b < 60; b++ {
		n := 6
		if b == 55 {
			n = 600
		}
		bt := t0.Add(time.Duration(b) * time.Minute)

		for i := 0; i < n; i++ {
			pts = append(pts, core.DataPoint{Time: bt.Add(time.Duration(i) * 90 * time.Millisecond)})
		}
	}
	job := jobspec.Job{Name: "r", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncRate, Side: jobspec.SideHigh}}}
	eng, err := New(job)
	if err != nil {
		t.Fatal(err)
	}
	var hit *core.Record
	for _, br := range eng.Run(pts, 50) {
		for i := range br.Records {
			if br.Records[i].Time.Equal(t0.Add(55 * time.Minute)) {
				hit = &br.Records[i]
			}
		}
	}
	if hit == nil {
		t.Fatal("rate burst bucket was not flagged")
	}
	if hit.Actual < 9 || hit.Actual > 11 {
		t.Fatalf("rate actual should be ~10 events/s, got %v", hit.Actual)
	}
}

func TestNonNullSumAndMetricAggregate(t *testing.T) {
	pts := []core.DataPoint{
		{Values: map[string]float64{"v": 2}},
		{Values: map[string]float64{"v": 3}},
		{Values: map[string]float64{"v": 5}},
	}
	if s, ok := detect.Aggregate(jobspec.FuncNonNullSum, "v", pts); !ok || s != 10 {
		t.Fatalf("non_null_sum = %v (ok=%v), want 10", s, ok)
	}
	if m, ok := detect.Aggregate(jobspec.FuncMetric, "v", pts); !ok || m < 3.33 || m > 3.34 {
		t.Fatalf("metric(mean) = %v (ok=%v), want ~3.333", m, ok)
	}
}

func TestFreqRareWeightsByFrequency(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint

	for b := 0; b < 30; b++ {
		bt := t0.Add(time.Duration(b) * time.Minute)
		pts = append(pts, core.DataPoint{Time: bt, Fields: map[string]string{"code": "ok"}})
	}

	b30 := t0.Add(30 * time.Minute)
	pts = append(pts, core.DataPoint{Time: b30, Fields: map[string]string{"code": "loner"}})
	b31 := t0.Add(31 * time.Minute)
	for i := 0; i < 200; i++ {
		pts = append(pts, core.DataPoint{Time: b31.Add(time.Duration(i) * time.Millisecond),
			Fields: map[string]string{"code": "storm"}})
	}
	job := jobspec.Job{Name: "fr", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncFreqRare, ByField: "code"}}}
	eng, err := New(job)
	if err != nil {
		t.Fatal(err)
	}
	var loner, storm float64
	for _, br := range eng.Run(pts, 50) {
		for _, r := range br.Records {
			switch r.Series {
			case "loner":
				loner = r.Score
			case "storm":
				storm = r.Score
			}
		}
	}
	if loner == 0 || storm == 0 {
		t.Fatalf("both rare values should be flagged: loner=%v storm=%v", loner, storm)
	}
	if storm <= loner {
		t.Fatalf("freq_rare should score the high-frequency value higher: loner=%v storm=%v", loner, storm)
	}
}
