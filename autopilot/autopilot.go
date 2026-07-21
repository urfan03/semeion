// Package autopilot removes the biggest adoption barrier of Elastic ML: hand-
// configuring every job. Given a sample of data it infers a sensible job —
// bucket span from the data cadence, a seasonal metric detector per numeric
// field, a joint multivariate detector when there are several, and a count
// detector — so anomaly detection works with zero configuration.
package autopilot

import (
	"sort"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

// niceSpans are the bucket spans the inferrer snaps to.
var niceSpans = []time.Duration{
	time.Second, 5 * time.Second, 10 * time.Second, 15 * time.Second, 30 * time.Second,
	time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour,
}

// Suggest infers a job from a sample of points.
func Suggest(points []core.DataPoint) jobspec.Job {
	span := suggestSpan(points)
	fields := numericFields(points)
	seasonal := len(points) >= 60 // enough history to learn a cycle

	dets := []jobspec.Detector{{Function: jobspec.FuncCount}}
	for _, f := range fields {
		dets = append(dets, jobspec.Detector{Function: jobspec.FuncMean, Field: f, Seasonal: seasonal})
	}
	if len(fields) >= 2 {
		dets = append(dets, jobspec.Detector{Fields: fields})
	}
	return jobspec.Job{Name: "autopilot", BucketSpan: span, Detectors: dets}
}

// suggestSpan snaps the median inter-arrival time to the nearest nice span.
func suggestSpan(points []core.DataPoint) time.Duration {
	if len(points) < 2 {
		return time.Minute
	}
	times := make([]time.Time, len(points))
	for i, p := range points {
		times[i] = p.Time
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	deltas := make([]float64, 0, len(times))
	for i := 1; i < len(times); i++ {
		if d := times[i].Sub(times[i-1]); d > 0 {
			deltas = append(deltas, float64(d))
		}
	}
	if len(deltas) == 0 {
		return time.Minute
	}
	sort.Float64s(deltas)
	median := time.Duration(deltas[len(deltas)/2])

	best, bestDiff := niceSpans[0], time.Duration(1<<62)
	for _, s := range niceSpans {
		diff := s - median
		if diff < 0 {
			diff = -diff
		}
		if diff < bestDiff {
			best, bestDiff = s, diff
		}
	}
	return best
}

// numericFields is the sorted union of names appearing in points' Values.
func numericFields(points []core.DataPoint) []string {
	set := map[string]bool{}
	for _, p := range points {
		for k := range p.Values {
			set[k] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
