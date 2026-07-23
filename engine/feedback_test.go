package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestFeedbackSuppressesMarkedSeries(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mkPts := func() []core.DataPoint {
		var pts []core.DataPoint
		for b := 0; b < 60; b++ {
			bt := t0.Add(time.Duration(b) * time.Minute)
			v := 100.0 + float64((b%7)-3)*4
			if b%10 == 0 {
				v = 170
			}
			pts = append(pts, core.DataPoint{Time: bt, Value: v, Fields: map[string]string{"host": "a"}, Values: map[string]float64{"v": v}})
		}
		return pts
	}
	job := jobspec.Job{Name: "f", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", ByField: "host", Side: jobspec.SideHigh}}}

	count := func(mark bool) int {
		eng, err := New(job)
		if err != nil {
			t.Fatal(err)
		}
		if mark {
			for i := 0; i < 5; i++ {
				eng.MarkFalsePositive("mean(v)", "host=a")
			}
		}
		n := 0
		for _, br := range eng.Run(mkPts(), 50) {
			n += len(br.Records)
		}
		return n
	}
	base := count(false)
	suppressed := count(true)
	if base == 0 {
		t.Fatal("fixture should produce anomalies without feedback")
	}
	if suppressed >= base {
		t.Fatalf("feedback should reduce reported anomalies: base=%d marked=%d", base, suppressed)
	}
}
