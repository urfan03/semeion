package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestPopulationOverPlusPartition(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	add := func(bt time.Time, region, user string, v float64) {
		pts = append(pts, core.DataPoint{Time: bt, Value: v,
			Fields: map[string]string{"region": region, "user": user},
			Values: map[string]float64{"v": v}})
	}
	for b := 0; b < 40; b++ {
		bt := t0.Add(time.Duration(b) * time.Minute)
		for u := 0; u < 5; u++ {
			add(bt, "A", "a"+string(rune('0'+u)), 100)
			add(bt, "B", "b"+string(rune('0'+u)), 1000) // region B lives at a much higher level
		}
		if b == 30 {
			add(bt, "A", "rogue", 500) // anomalous FOR REGION A, but between A's and B's levels
		}
	}
	job := jobspec.Job{Name: "pop", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", OverField: "user", PartitionField: "region"}}}
	eng, err := New(job)
	if err != nil {
		t.Fatal(err)
	}
	flaggedRogue := false
	falseInB := false
	for _, br := range eng.Run(pts, 50) {
		for _, r := range br.Records {
			if strings.Contains(r.Series, "user=rogue") {
				flaggedRogue = true
			}
			if strings.Contains(r.Series, "region=B") {
				falseInB = true
			}
		}
	}
	if !flaggedRogue {
		t.Fatal("an entity anomalous within its own partition's pool (region A) must be flagged")
	}
	if falseInB {
		t.Fatal("region B's normal (high) users must not be flagged against A's pool — pools must be per-partition")
	}
}
