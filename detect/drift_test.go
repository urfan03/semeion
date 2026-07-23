package detect

import (
	"testing"

	"github.com/urfan03/semeion/jobspec"
)

func TestConceptDriftRebase(t *testing.T) {
	m := NewModel(jobspec.SideHigh)
	for i := 0; i < 60; i++ {
		m.Learn(100)
	}

	for i := 0; i < driftBuckets+30; i++ {
		m.Observe(300)
	}

	_, score, _, _ := m.Observe(300)
	if score > 25 {
		t.Fatalf("a long-sustained new level should be rebased to normal, got %.1f", score)
	}

	_, spike, _, _ := m.Observe(900)
	if spike < 60 {
		t.Fatalf("a jump above the rebased normal should flag, got %.1f", spike)
	}
}

func TestShortShiftNotRebased(t *testing.T) {
	m := NewModel(jobspec.SideHigh)
	for i := 0; i < 60; i++ {
		m.Learn(100)
	}
	var last float64
	for i := 0; i < 30; i++ {
		_, last, _, _ = m.Observe(300)
	}
	if last < 40 {
		t.Fatalf("a short sustained shift must still be anomalous, got %.1f", last)
	}
}
