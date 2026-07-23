package detect

import (
	"testing"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestModelWarmupSpikeAndReturn(t *testing.T) {
	m := NewModel(jobspec.SideBoth)

	baseline := []float64{98, 100, 102, 101, 99, 100, 103, 97}
	for i := 0; i < 40; i++ {
		m.Observe(baseline[i%len(baseline)])
	}

	_, spikeScore, _, dir := m.Observe(300)
	if spikeScore < 50 {
		t.Fatalf("spike: expected score ≥ 50, got %.1f", spikeScore)
	}
	if dir != core.DirUp {
		t.Fatalf("spike: expected direction up, got %s", dir)
	}

	_, normalScore, _, _ := m.Observe(100)
	if normalScore > 25 {
		t.Fatalf("normal value: expected score ≤ 25, got %.1f", normalScore)
	}
}

func TestModelHighSideIgnoresDips(t *testing.T) {
	m := NewModel(jobspec.SideHigh)
	for i := 0; i < 40; i++ {
		m.Observe(100)
	}
	_, dipScore, _, _ := m.Observe(1)
	if dipScore > 25 {
		t.Fatalf("high-side dip: expected low score, got %.1f", dipScore)
	}
	_, spikeScore, _, _ := m.Observe(1000)
	if spikeScore < 50 {
		t.Fatalf("high-side spike: expected score ≥ 50, got %.1f", spikeScore)
	}
}

func TestModelWarmupSilent(t *testing.T) {
	m := NewModel(jobspec.SideBoth)
	for i := 0; i < defaultWarmup-1; i++ {
		if _, score, _, _ := m.Observe(100); score != 0 {
			t.Fatalf("warm-up: expected score 0, got %.1f at i=%d", score, i)
		}
	}
}
