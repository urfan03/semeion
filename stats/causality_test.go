package stats

import (
	"math"
	"testing"
)

// #7: a series whose movements are echoed by a second series a few steps later
// is detected as LEADING it, at the correct lag.
func TestLeadLagDetectsLead(t *testing.T) {
	n := 300
	a := make([]float64, n)
	b := make([]float64, n)
	for i := 0; i < n; i++ {
		a[i] = math.Sin(float64(i) * 0.3)
	}
	lead := 4
	for i := 0; i < n; i++ {
		if i-lead >= 0 {
			b[i] = a[i-lead] // b is a delayed by `lead` steps → a leads b
		}
	}
	lag, corr := LeadLag(a, b, 10)
	if lag != lead {
		t.Fatalf("expected a to lead b by %d, got lag %d (corr %.2f)", lead, lag, corr)
	}
	if corr < 0.9 {
		t.Fatalf("peak correlation should be strong, got %.2f", corr)
	}
}

// #7: Granger — a's past improves prediction of b (b := delayed a + noise) more
// than b's past predicts a.
func TestGrangerDirection(t *testing.T) {
	n := 400
	a := make([]float64, n)
	b := make([]float64, n)
	// Deterministic white innovations (xorshift) — a genuinely unpredictable
	// driver, so Granger has something to attribute. A pure sine is perfectly
	// self-predictable and would show no causality in either direction.
	seed := uint64(88172645463325252)
	next := func() float64 {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return float64(seed%2000)/1000 - 1 // ~U(-1,1)
	}
	for i := 0; i < n; i++ {
		a[i] = next()
	}
	for i := 2; i < n; i++ {
		b[i] = 0.8*a[i-2] + 0.05*next() // b driven by a's past
	}
	abImprove, _ := Granger(a, b, 3) // a → b should explain a lot
	baImprove, _ := Granger(b, a, 3) // b → a should explain little
	if abImprove <= baImprove {
		t.Fatalf("a should Granger-cause b more than the reverse: a→b=%.3f b→a=%.3f", abImprove, baImprove)
	}
	if abImprove < 0.2 {
		t.Fatalf("a→b Granger improvement should be substantial, got %.3f", abImprove)
	}
}
