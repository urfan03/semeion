package detect

import (
	"math"
	"testing"
	"time"

	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/model"
)

func TestSeasonalCatchesPhaseAnomaly(t *testing.T) {
	const period = 12
	val := func(i int) float64 { return 100 + 50*math.Cos(2*math.Pi*float64(i)/float64(period)) }

	span := time.Minute
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(i int) time.Time { return t0.Add(time.Duration(i) * span) }

	sm := NewSeasonalModel(jobspec.SideBoth, model.NewGoProvider(), span)
	plain := NewModel(jobspec.SideBoth)

	n := 40 * period
	for i := 0; i < n; i++ {
		sm.Observe(at(i), val(i))
		plain.Observe(val(i))
	}
	if sm.Period() != period {
		t.Fatalf("expected detected period %d, got %d", period, sm.Period())
	}

	_, seasonalScore, _, _ := sm.Observe(at(n), 55)
	_, plainScore, _, _ := plain.Observe(55)

	if seasonalScore < 50 {
		t.Fatalf("seasonal model should flag the phase anomaly, score=%.1f", seasonalScore)
	}
	if plainScore > 25 {
		t.Fatalf("plain model should NOT flag 55 (within global range), score=%.1f", plainScore)
	}
}
