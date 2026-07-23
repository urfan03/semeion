package detect

import (
	"testing"

	"github.com/urfan03/semeion/jobspec"
)

// #3: a permanent level shift eventually becomes the new normal (rebase), while
// a SHORT sustained shift is still flagged (not prematurely accepted).
func TestConceptDriftRebase(t *testing.T) {
	m := NewModel(jobspec.SideHigh)
	for i := 0; i < 60; i++ {
		m.Learn(100) // baseline ~100
	}
	// A shift to 300 that lasts well beyond driftBuckets → accepted as new normal.
	for i := 0; i < driftBuckets+30; i++ {
		m.Observe(300)
	}
	// After rebasing, a fresh 300 (the new normal) is NOT anomalous.
	_, score, _, _ := m.Observe(300)
	if score > 25 {
		t.Fatalf("a long-sustained new level should be rebased to normal, got %.1f", score)
	}
	// But a jump above the NEW normal is still caught.
	_, spike, _, _ := m.Observe(900)
	if spike < 60 {
		t.Fatalf("a jump above the rebased normal should flag, got %.1f", spike)
	}
}

// A short sustained shift (below driftBuckets) is NOT rebased — it keeps scoring
// as anomalous, so a real outage isn't silently swallowed.
func TestShortShiftNotRebased(t *testing.T) {
	m := NewModel(jobspec.SideHigh)
	for i := 0; i < 60; i++ {
		m.Learn(100)
	}
	var last float64
	for i := 0; i < 30; i++ { // 30 « driftBuckets
		_, last, _, _ = m.Observe(300)
	}
	if last < 40 {
		t.Fatalf("a short sustained shift must still be anomalous, got %.1f", last)
	}
}
