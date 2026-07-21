// Package ingest holds data sources for the engine. Synthetic is a deterministic
// generator used by the demo and tests (no randomness → reproducible). Real
// datafeeds (Elasticsearch, Prometheus, …) arrive in the adapters phase.
package ingest

import (
	"math"
	"time"

	"github.com/urfan03/semeion/core"
)

// Synthetic builds a deterministic series: a daily sinusoid + a fixed periodic
// "noise" term, over n buckets of the given span. Any index present in
// anomalies has its value multiplied by the given factor (>1 = spike, <1 = dip),
// letting tests assert the engine catches injected anomalies.
func Synthetic(start time.Time, span time.Duration, n int, anomalies map[int]float64) []core.DataPoint {
	const bucketsPerDay = 288 // 5-minute buckets in a day
	out := make([]core.DataPoint, 0, n)
	for i := 0; i < n; i++ {
		t := start.Add(time.Duration(i) * span)
		phase := float64(i%bucketsPerDay) / bucketsPerDay * 2 * math.Pi
		base := 100 + 40*math.Sin(phase)
		noise := 5 * math.Sin(float64(i)*0.7) // deterministic, reproducible
		v := base + noise
		if mult, ok := anomalies[i]; ok {
			v *= mult
		}
		out = append(out, core.DataPoint{Time: t, Value: v})
	}
	return out
}
