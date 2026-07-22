package detect

import (
	"testing"

	"github.com/urfan03/semeion/jobspec"
)

// C2 regression: a larger past spike still in the window must NOT hide a new,
// smaller anomaly. Before the fix, MAD==0 fell back to full-window std, which
// the past spikes inflated, crushing the new anomaly's score (measured: 4).
func TestFlatBaselineWithPastSpikeStillFlagsNewAnomaly(t *testing.T) {
	m := NewModel(jobspec.SideBoth)
	// 40 counts at 100, then 5 past spikes at 900 — median 100, MAD 0.
	for i := 0; i < 40; i++ {
		m.Learn(100)
	}
	for i := 0; i < 5; i++ {
		m.Learn(900)
	}
	_, score, _, _ := m.Observe(300) // a real, moderate new anomaly
	if score < 90 {
		t.Fatalf("a new 300 on a 100-median baseline should score high, got %.1f", score)
	}
}

// The constant-continuous baseline uses a relative floor: the same *relative*
// jump scores the same regardless of absolute level (fixes the level-dependence).
func TestConstantBaselineScoreIsLevelInvariant(t *testing.T) {
	score := func(level, jump float64) float64 {
		m := NewModel(jobspec.SideHigh)
		for i := 0; i < 40; i++ {
			m.Learn(level + 0.5) // .5 → non-integer, forces the continuous floor
		}
		_, s, _, _ := m.Observe(level + 0.5 + jump)
		return s
	}
	// +20% at level 100 and at level 1000 should score within a small margin.
	a := score(100, 20)
	b := score(1000, 200)
	if diff := a - b; diff < -5 || diff > 5 {
		t.Fatalf("relative jump not level-invariant: score(100,+20)=%.1f score(1000,+200)=%.1f", a, b)
	}
	if a < 50 {
		t.Fatalf("a +20%% jump on a constant baseline should be clearly anomalous, got %.1f", a)
	}
}
