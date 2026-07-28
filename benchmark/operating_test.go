package benchmark

import (
	"math"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
)

func syntheticSeries(key string, n int, events [][2]int, scores []float64) (CorpusSeries, []float64) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pts := make([]core.DataPoint, n)
	labels := make([]bool, n)
	for i := range pts {
		pts[i] = core.DataPoint{Time: base.Add(time.Duration(i) * time.Minute)}
	}
	count := 0
	for _, e := range events {
		for i := e[0]; i <= e[1] && i < n; i++ {
			labels[i] = true
			count++
		}
	}
	return CorpusSeries{Key: key, Points: pts, Labels: labels, Anomalies: count}, scores
}

func TestEventScoreCountsEventsNotPoints(t *testing.T) {
	n := 100
	scores := make([]float64, n)
	for i := 20; i <= 29; i++ {
		scores[i] = 9
	}
	for i := 60; i <= 69; i++ {
		scores[i] = 9
	}
	scores[80] = 9
	s, sc := syntheticSeries("a", n, [][2]int{{20, 29}, {60, 69}}, scores)

	fn := func(CorpusSeries) []float64 { return sc }
	alarm := func(_ CorpusSeries, x []float64) []bool {
		out := make([]bool, len(x))
		for i, v := range x {
			out[i] = v >= 1
		}
		return out
	}
	op := EventScore([]CorpusSeries{s}, fn, alarm, "raw")
	if op.Events != 2 || op.EventsHit != 2 {
		t.Fatalf("both windows must be caught: %+v", op)
	}
	if op.Alarms != 21 || op.FalseAlarms != 1 {
		t.Fatalf("expected 21 alarms with 1 outside a window: %+v", op)
	}
	if math.Abs(op.EventRecall-1) > 1e-9 {
		t.Fatalf("event recall must be 1, got %v", op.EventRecall)
	}
	if math.Abs(op.AlarmPrecision-20.0/21.0) > 1e-9 {
		t.Fatalf("alarm precision wrong: %v", op.AlarmPrecision)
	}
	if op.Series != 1 || math.Abs(op.AlarmsPerSerie-21) > 1e-9 {
		t.Fatalf("per-series alarm rate wrong: %+v", op)
	}
	if op.F1 <= 0.9 {
		t.Fatalf("F1 should be near 1 here, got %v", op.F1)
	}
}

func TestEventScoreSkipsUnusable(t *testing.T) {
	clean, sc := syntheticSeries("clean", 50, nil, make([]float64, 50))
	fn := func(CorpusSeries) []float64 { return sc }
	all := func(_ CorpusSeries, x []float64) []bool {
		out := make([]bool, len(x))
		for i := range out {
			out[i] = true
		}
		return out
	}
	op := EventScore([]CorpusSeries{clean}, fn, all, "x")
	if op.Series != 0 || op.Events != 0 {
		t.Fatalf("an unlabelled series must not be scored: %+v", op)
	}

	labelled, _ := syntheticSeries("bad", 50, [][2]int{{10, 12}}, nil)
	short := EventScore([]CorpusSeries{labelled}, func(CorpusSeries) []float64 { return []float64{1} }, all, "x")
	if short.Series != 0 {
		t.Fatalf("a detector returning the wrong length must be skipped: %+v", short)
	}
}

func TestCurveOrdersByRecall(t *testing.T) {
	n := 200
	scores := make([]float64, n)
	for i := range scores {
		scores[i] = 1
	}
	for i := 50; i <= 59; i++ {
		scores[i] = 5
	}
	for i := 150; i <= 159; i++ {
		scores[i] = 3
	}
	s, sc := syntheticSeries("a", n, [][2]int{{50, 59}, {150, 159}}, scores)
	fn := func(CorpusSeries) []float64 { return sc }

	at := func(thr float64) AlarmFunc {
		return func(_ CorpusSeries, x []float64) []bool {
			out := make([]bool, len(x))
			for i, v := range x {
				out[i] = v >= thr
			}
			return out
		}
	}
	curve := Curve([]CorpusSeries{s}, fn, []AlarmFunc{at(4), at(2), at(0.5)}, []string{"tight", "mid", "loose"})
	if len(curve) != 3 {
		t.Fatalf("expected 3 points, got %d", len(curve))
	}
	for i := 1; i < len(curve); i++ {
		if curve[i].EventRecall < curve[i-1].EventRecall {
			t.Fatalf("the curve must be sorted by recall: %+v", curve)
		}
	}
	if curve[0].Label != "tight" || curve[0].EventRecall != 0.5 {
		t.Fatalf("the tight threshold should catch one of two windows: %+v", curve[0])
	}
	if curve[len(curve)-1].AlarmPrecision >= curve[0].AlarmPrecision {
		t.Fatalf("loosening the threshold must cost precision: %+v", curve)
	}
}
