package ingest

import (
	"math"
	"time"

	"github.com/urfan03/semeion/core"
)

func Synthetic(start time.Time, span time.Duration, n int, anomalies map[int]float64) []core.DataPoint {
	const bucketsPerDay = 288
	out := make([]core.DataPoint, 0, n)
	for i := 0; i < n; i++ {
		t := start.Add(time.Duration(i) * span)
		phase := float64(i%bucketsPerDay) / bucketsPerDay * 2 * math.Pi
		base := 100 + 40*math.Sin(phase)
		noise := 5 * math.Sin(float64(i)*0.7)
		v := base + noise
		if mult, ok := anomalies[i]; ok {
			v *= mult
		}
		out = append(out, core.DataPoint{Time: t, Value: v})
	}
	return out
}
