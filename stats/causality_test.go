package stats

import (
	"math"
	"testing"
)

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
			b[i] = a[i-lead]
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

func TestGrangerDirection(t *testing.T) {
	n := 400
	a := make([]float64, n)
	b := make([]float64, n)

	seed := uint64(88172645463325252)
	next := func() float64 {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return float64(seed%2000)/1000 - 1
	}
	for i := 0; i < n; i++ {
		a[i] = next()
	}
	for i := 2; i < n; i++ {
		b[i] = 0.8*a[i-2] + 0.05*next()
	}
	abImprove, _ := Granger(a, b, 3)
	baImprove, _ := Granger(b, a, 3)
	if abImprove <= baImprove {
		t.Fatalf("a should Granger-cause b more than the reverse: a→b=%.3f b→a=%.3f", abImprove, baImprove)
	}
	if abImprove < 0.2 {
		t.Fatalf("a→b Granger improvement should be substantial, got %.3f", abImprove)
	}
}

func TestGrangerStableOnLargeCollinear(t *testing.T) {
	n := 200
	a := make([]float64, n)
	b := make([]float64, n)
	seed := uint64(99)
	next := func() float64 {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return float64(seed%2000)/1000 - 1
	}
	acc := 1e6
	for i := 0; i < n; i++ {
		acc += next()
		a[i] = acc
		b[i] = a[i]*1.0000001 + 1e-3*next()
	}
	imp, f := Granger(a, b, 3)
	if math.IsNaN(imp) || math.IsInf(imp, 0) || imp < 0 || imp > 1.0001 {
		t.Fatalf("Granger improvement must stay finite in [0,1] on large collinear input, got %v", imp)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		t.Fatalf("Granger F must be finite and non-negative, got %v", f)
	}
}
