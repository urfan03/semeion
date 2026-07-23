package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestDelayedDataGrace(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	job := jobspec.Job{Name: "d", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", Side: jobspec.SideHigh}}}
	pt := func(b int, v float64) core.DataPoint {
		return core.DataPoint{Time: t0.Add(time.Duration(b) * time.Minute), Value: v, Values: map[string]float64{"v": v}}
	}

	run := func(grace time.Duration) (accepted, dropped int64, bucket28 *core.Record) {
		eng, _ := New(job)
		eng.Grace = grace
		for b := 0; b <= 30; b++ {
			eng.Push(pt(b, 100))
		}
		eng.Push(pt(28, 900)) // arrives 2 buckets late
		var out []core.BucketResult
		out = append(out, eng.Flush()...)
		for i := range out {
			if out[i].Time.Equal(t0.Add(28 * time.Minute)) {
				for j := range out[i].Records {
					bucket28 = &out[i].Records[j]
				}
			}
		}
		return eng.LateAccepted, eng.LateDropped, bucket28
	}

	acc, drop, rec := run(5 * time.Minute)
	if acc != 1 || drop != 0 {
		t.Fatalf("within grace, the late point should be accepted not dropped: accepted=%d dropped=%d", acc, drop)
	}
	if rec == nil || rec.Actual < 450 || rec.Actual > 550 {
		t.Fatalf("bucket 28 should be re-scored with the folded late value (mean ~500): %+v", rec)
	}

	acc0, drop0, _ := run(0)
	if drop0 != 1 || acc0 != 0 {
		t.Fatalf("with no grace, a 2-bucket-late point must be dropped: accepted=%d dropped=%d", acc0, drop0)
	}
}
