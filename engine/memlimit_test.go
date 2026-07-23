package engine

import (
	"fmt"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestModelMemoryBounded(t *testing.T) {
	job := jobspec.Job{Name: "hc", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", ByField: "id"}}}
	eng, err := New(job)
	if err != nil {
		t.Fatal(err)
	}
	eng.MaxSeries = 100

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 5000; i++ {
		id := fmt.Sprintf("id-%d", i)
		eng.Run([]core.DataPoint{{
			Time: t0.Add(time.Duration(i) * time.Minute), Value: 100,
			Fields: map[string]string{"id": id}, Values: map[string]float64{"v": 100},
		}}, 50)
	}
	if n := len(eng.models); n > 120 {
		t.Fatalf("model map grew unbounded: %d resident (cap 100)", n)
	}
	if len(eng.seriesLRU) > 120 {
		t.Fatalf("LRU tracker grew unbounded: %d", len(eng.seriesLRU))
	}
	if eng.Evicted == 0 {
		t.Fatal("expected evictions under a high-cardinality field")
	}
}
