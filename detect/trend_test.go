package detect

import (
	"testing"

	"github.com/urfan03/semeion/jobspec"
)

func TestTrendAwareBaselineIgnoresOnTrendGrowth(t *testing.T) {
	m := NewModel(jobspec.SideHigh)
	for i := 0; i < 60; i++ {
		m.Learn(float64(100 + 2*i))
	}

	_, score, _, _ := m.Observe(220)
	if score > 25 {
		t.Fatalf("on-trend growth should not be flagged, got %.1f", score)
	}

	m2 := NewModel(jobspec.SideHigh)
	for i := 0; i < 60; i++ {
		m2.Learn(float64(100 + 2*i))
	}
	_, spikeScore, _, _ := m2.Observe(400)
	if spikeScore < 60 {
		t.Fatalf("a jump above the trend should be flagged, got %.1f", spikeScore)
	}
}

func TestModelBounds(t *testing.T) {
	m := NewModel(jobspec.SideBoth)
	for i := 0; i < 60; i++ {
		m.Learn(100)
		if i%2 == 0 {
			m.Learn(102)
		}
	}
	m.Observe(200)
	lo, hi := m.Bounds(1.96)
	if lo >= hi {
		t.Fatalf("bounds must be ordered: [%.2f, %.2f]", lo, hi)
	}
	if !(200 > hi) {
		t.Fatalf("a spike of 200 should sit above the upper bound %.2f", hi)
	}
}

func TestWarmupRamp(t *testing.T) {
	m := NewModel(jobspec.SideHigh)

	for i := 0; i < 20; i++ {
		m.Learn(100)
	}

	early := m.copyForPeek()
	_, earlyScore, _, _ := early.Observe(1000)

	for i := 0; i < 10; i++ {
		m.Learn(100)
	}
	_, lateScore, _, _ := m.Observe(1000)
	if earlyScore >= lateScore {
		t.Fatalf("score should ramp up after warm-up: early=%.1f late=%.1f", earlyScore, lateScore)
	}
}

func (m *Model) copyForPeek() *Model {
	c := *m
	c.history = append([]float64(nil), m.history...)
	c.recent = append([]float64(nil), m.recent...)
	return &c
}
