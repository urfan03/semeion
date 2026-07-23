package detect

import (
	"testing"

	"github.com/urfan03/semeion/jobspec"
)

func TestFlatBaselineWithPastSpikeStillFlagsNewAnomaly(t *testing.T) {
	m := NewModel(jobspec.SideBoth)

	for i := 0; i < 40; i++ {
		m.Learn(100)
	}
	for i := 0; i < 5; i++ {
		m.Learn(900)
	}
	_, score, _, _ := m.Observe(300)
	if score < 90 {
		t.Fatalf("a new 300 on a 100-median baseline should score high, got %.1f", score)
	}
}

func TestConstantBaselineScoreIsLevelInvariant(t *testing.T) {
	score := func(level, jump float64) float64 {
		m := NewModel(jobspec.SideHigh)
		for i := 0; i < 40; i++ {
			m.Learn(level + 0.5)
		}
		_, s, _, _ := m.Observe(level + 0.5 + jump)
		return s
	}

	a := score(100, 20)
	b := score(1000, 200)
	if diff := a - b; diff < -5 || diff > 5 {
		t.Fatalf("relative jump not level-invariant: score(100,+20)=%.1f score(1000,+200)=%.1f", a, b)
	}
	if a < 50 {
		t.Fatalf("a +20%% jump on a constant baseline should be clearly anomalous, got %.1f", a)
	}
}
