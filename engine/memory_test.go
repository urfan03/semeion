package engine

import (
	"fmt"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestMemoryBoundedUnderHighCardinality(t *testing.T) {
	job := jobspec.Job{Name: "hc", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", ByField: "host", Side: jobspec.SideHigh}}}
	eng, err := New(job)
	if err != nil {
		t.Fatal(err)
	}
	eng.MaxSeries = 500

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const hosts = 6000
	var pts []core.DataPoint
	for i := 0; i < hosts; i++ {
		pts = append(pts, core.DataPoint{
			Time:   t0.Add(time.Duration(i/50) * time.Minute),
			Value:  100,
			Fields: map[string]string{"host": fmt.Sprintf("h%d", i)},
			Values: map[string]float64{"v": 100},
		})
	}
	eng.Run(pts, 50)

	if len(eng.models) > eng.MaxSeries {
		t.Fatalf("resident models %d exceeded MaxSeries %d", len(eng.models), eng.MaxSeries)
	}
	if len(eng.seriesLRU) > eng.MaxSeries {
		t.Fatalf("LRU table %d exceeded MaxSeries %d", len(eng.seriesLRU), eng.MaxSeries)
	}
	if eng.Evicted == 0 {
		t.Fatal("high-cardinality feed should have triggered eviction")
	}
	if len(eng.lastSeen) > eng.MaxSeries {
		t.Fatalf("lastSeen table %d exceeded MaxSeries %d (leak)", len(eng.lastSeen), eng.MaxSeries)
	}
}
