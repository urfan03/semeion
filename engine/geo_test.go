package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

// #8: a lat_long detector learns a user's usual location (logins from London)
// and flags a login from a far-away place (Sydney) as a geographic anomaly.
func TestLatLongDetectsImpossibleTravel(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	london := [2]float64{51.5074, -0.1278}
	var pts []core.DataPoint
	// 60 buckets of logins near London (small jitter), one series (user=alice).
	for b := 0; b < 60; b++ {
		bt := t0.Add(time.Duration(b) * time.Minute)
		jit := float64(b%5) * 0.01
		pts = append(pts, core.DataPoint{Time: bt, Fields: map[string]string{"user": "alice"},
			Values: map[string]float64{"lat": london[0] + jit, "lon": london[1] + jit}})
	}
	// Bucket 60: a login from Sydney.
	syd := t0.Add(60 * time.Minute)
	pts = append(pts, core.DataPoint{Time: syd, Fields: map[string]string{"user": "alice"},
		Values: map[string]float64{"lat": -33.8688, "lon": 151.2093}})

	job := jobspec.Job{Name: "geo", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncLatLong, ByField: "user"}}}
	eng, err := New(job)
	if err != nil {
		t.Fatal(err)
	}
	var hit *core.Record
	for _, br := range eng.Run(pts, 50) {
		if !br.Time.Equal(syd) {
			continue
		}
		for i := range br.Records {
			hit = &br.Records[i]
		}
	}
	if hit == nil {
		t.Fatal("a login from Sydney against a London baseline should be flagged")
	}
	if hit.Kind != "lat_long" {
		t.Fatalf("record kind should be lat_long, got %q", hit.Kind)
	}
	if hit.Actual < 10000 { // London→Sydney ≈ 17000 km
		t.Fatalf("actual distance should be ~17000 km, got %.0f", hit.Actual)
	}
}

// #8: staying near the learned centroid is not flagged.
func TestLatLongStableLocationNoAnomaly(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	for b := 0; b < 80; b++ {
		bt := t0.Add(time.Duration(b) * time.Minute)
		jit := float64(b%7) * 0.02
		pts = append(pts, core.DataPoint{Time: bt, Fields: map[string]string{"user": "bob"},
			Values: map[string]float64{"lat": 40.71 + jit, "lon": -74.0 + jit}})
	}
	job := jobspec.Job{Name: "geo", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncLatLong, ByField: "user"}}}
	eng, _ := New(job)
	n := 0
	for _, br := range eng.Run(pts, 50) {
		n += len(br.Records)
	}
	if n != 0 {
		t.Fatalf("a stable location should produce no geo anomalies, got %d", n)
	}
}
