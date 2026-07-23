package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestStaleSeriesWatchdog(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	job := jobspec.Job{Name: "s", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", ByField: "host"}}}
	eng, err := New(job)
	if err != nil {
		t.Fatal(err)
	}
	var pts []core.DataPoint
	for b := 0; b < 30; b++ {
		bt := t0.Add(time.Duration(b) * time.Minute)
		pts = append(pts, core.DataPoint{Time: bt, Value: 100, Fields: map[string]string{"host": "a"}, Values: map[string]float64{"v": 100}})
		if b < 10 {
			pts = append(pts, core.DataPoint{Time: bt, Value: 100, Fields: map[string]string{"host": "b"}, Values: map[string]float64{"v": 100}})
		}
	}
	eng.Run(pts, 50)

	stale := eng.Stale(10 * time.Minute)
	foundB := false
	for _, s := range stale {
		if strings.Contains(s.Series, "host=b") {
			foundB = true
		}
		if strings.Contains(s.Series, "host=a") {
			t.Fatalf("host=a is still active and must not be stale: %+v", s)
		}
	}
	if !foundB {
		t.Fatalf("host=b went silent after bucket 10 (~20m ago) and should be stale: %+v", stale)
	}
}
