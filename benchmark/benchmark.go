// Package benchmark measures detection quality: it builds labelled series (or
// accepts external ones, e.g. NAB / Yahoo S5) and scores an engine's output
// with windowed precision / recall / F1. This is the objective quality gate the
// project is held to.
package benchmark

import (
	"math"
	"time"

	"github.com/urfan03/semeion/core"
)

// Labeled is a series with ground-truth anomaly bucket-starts.
type Labeled struct {
	Points    []core.DataPoint
	Anomalies map[time.Time]bool
	Span      time.Duration
}

// Generate builds a deterministic, stationary noisy series with spikes injected
// at the given bucket indices; those buckets are labelled as anomalies.
func Generate(start time.Time, span time.Duration, n int, spikeAt []int, spikeMult float64) Labeled {
	lbl := Labeled{Span: span, Anomalies: make(map[time.Time]bool)}
	spikes := make(map[int]bool, len(spikeAt))
	for _, i := range spikeAt {
		spikes[i] = true
	}
	for i := 0; i < n; i++ {
		t := start.Add(time.Duration(i) * span)
		v := 100 + 6*math.Sin(float64(i)*0.9) + 3*math.Sin(float64(i)*0.37) // stationary noise
		if spikes[i] {
			v *= spikeMult
			lbl.Anomalies[t.Truncate(span)] = true
		}
		lbl.Points = append(lbl.Points, core.DataPoint{Time: t, Value: v})
	}
	return lbl
}

// ScoreResult holds windowed precision / recall / F1.
type ScoreResult struct {
	TP, FP, FN int
	Precision  float64
	Recall     float64
	F1         float64
}

// Score matches flagged buckets (those with ≥1 record) against labels within
// ±tolerance buckets. A flag near any label is a true positive; a label with no
// nearby flag is a miss; a flag with no nearby label is a false positive.
func Score(results []core.BucketResult, labels map[time.Time]bool, span time.Duration, tolerance int) ScoreResult {
	flagged := make(map[time.Time]bool)
	for _, br := range results {
		if len(br.Records) > 0 {
			flagged[br.Time] = true
		}
	}
	near := func(set map[time.Time]bool, t time.Time) bool {
		for d := -tolerance; d <= tolerance; d++ {
			if set[t.Add(time.Duration(d)*span)] {
				return true
			}
		}
		return false
	}
	var res ScoreResult
	for t := range labels {
		if near(flagged, t) {
			res.TP++
		} else {
			res.FN++
		}
	}
	for t := range flagged {
		if !near(labels, t) {
			res.FP++
		}
	}
	if res.TP+res.FP > 0 {
		res.Precision = float64(res.TP) / float64(res.TP+res.FP)
	}
	if res.TP+res.FN > 0 {
		res.Recall = float64(res.TP) / float64(res.TP+res.FN)
	}
	if res.Precision+res.Recall > 0 {
		res.F1 = 2 * res.Precision * res.Recall / (res.Precision + res.Recall)
	}
	return res
}
