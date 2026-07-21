package detect

import (
	"math"
	"testing"

	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/model"
)

// The marquee A3 property: a value that is normal GLOBALLY but abnormal for its
// phase (a trough-level reading at a peak time) is caught by the seasonal model,
// while the plain robust model — which only knows the global range — misses it.
func TestSeasonalCatchesPhaseAnomaly(t *testing.T) {
	const period = 12
	val := func(i int) float64 { return 100 + 50*math.Cos(2*math.Pi*float64(i)/float64(period)) } // range [50,150]

	sm := NewSeasonalModel(jobspec.SideBoth, model.NewGoProvider())
	plain := NewModel(jobspec.SideBoth)

	n := 40 * period // many full cycles; n % period == 0
	for i := 0; i < n; i++ {
		sm.Observe(val(i))
		plain.Observe(val(i))
	}
	if sm.Period() != period {
		t.Fatalf("expected detected period %d, got %d", period, sm.Period())
	}

	// Next observation is index n (phase 0 = the peak, expected ~150). Feed a
	// trough-level value; seasonal must flag it, plain must not.
	_, seasonalScore, _, _ := sm.Observe(55)
	_, plainScore, _, _ := plain.Observe(55)

	if seasonalScore < 50 {
		t.Fatalf("seasonal model should flag the phase anomaly, score=%.1f", seasonalScore)
	}
	if plainScore > 25 {
		t.Fatalf("plain model should NOT flag 55 (within global range), score=%.1f", plainScore)
	}
}
