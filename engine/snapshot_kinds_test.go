package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func feed(start time.Time, n int, val func(i int) float64) []core.DataPoint {
	pts := make([]core.DataPoint, n)
	for i := range pts {
		pts[i] = core.DataPoint{Time: start.Add(time.Duration(i) * time.Minute), Value: val(i),
			Values: map[string]float64{"a": val(i), "b": val(i) * 2}}
	}
	return pts
}

func TestSnapshotRestoreAllModelKinds(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := map[string]jobspec.Job{
		"distribution": {Name: "d", BucketSpan: time.Minute,
			Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", Distribution: true}}},
		"seasonal": {Name: "s", BucketSpan: time.Minute,
			Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", Seasonal: true}}},
		"multivariate": {Name: "m", BucketSpan: time.Minute,
			Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Fields: []string{"a", "b"}}}},
		"time_of_day": {Name: "t", BucketSpan: time.Minute,
			Detectors: []jobspec.Detector{{Function: jobspec.FuncTimeOfDay}}},
	}

	for name, job := range cases {
		t.Run(name, func(t *testing.T) {
			eng, err := New(job)
			if err != nil {
				t.Fatal(err)
			}

			pts := feed(start, 120, func(i int) float64 { return 100 + 10*float64(i%12) })
			for _, p := range pts {
				fieldPoint(&p, "v")
				eng.Push(p)
			}
			eng.Flush()

			snap := eng.Snapshot()

			switch name {
			case "distribution":
				if len(snap.Distrib) == 0 {
					t.Fatal("distribution state not captured in snapshot")
				}
			case "seasonal":
				if len(snap.Seasonal) == 0 {
					t.Fatal("seasonal state not captured in snapshot")
				}
			case "multivariate":
				if len(snap.Multivar) == 0 {
					t.Fatal("multivariate state not captured in snapshot")
				}
			case "time_of_day":
				if len(snap.Slots) == 0 {
					t.Fatal("time-of-day slot state not captured in snapshot")
				}
			}

			restored, err := New(job)
			if err != nil {
				t.Fatal(err)
			}
			restored.Restore(snap)
			rs := restored.Snapshot()
			if len(rs.Distrib) != len(snap.Distrib) || len(rs.Seasonal) != len(snap.Seasonal) ||
				len(rs.Multivar) != len(snap.Multivar) || len(rs.Slots) != len(snap.Slots) {
				t.Fatalf("restore lost model state: got distrib=%d seas=%d mv=%d slots=%d",
					len(rs.Distrib), len(rs.Seasonal), len(rs.Multivar), len(rs.Slots))
			}
		})
	}
}

func fieldPoint(p *core.DataPoint, field string) {
	if p.Values == nil {
		p.Values = map[string]float64{}
	}
	p.Values[field] = p.Value
}
