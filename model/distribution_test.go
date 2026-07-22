package model

import "testing"

// Symmetric data with negatives leaves only the normal candidate; its tail is
// ~1 at the centre and tiny far out.
func TestFitNormalAndTail(t *testing.T) {
	x := []float64{-3, -2, -1, 0, 1, 2, 3, -2.5, 2.5, -1.5, 1.5, 0.5, -0.5}
	d := NewGoProvider().FitDistribution(x)
	if d.Family != "normal" {
		t.Fatalf("family: got %q, want normal", d.Family)
	}
	if p := d.Tail(d.Params[0]); p < 0.5 {
		t.Fatalf("tail at mean should be high, got %.3f", p)
	}
	far := d.Params[0] + 8*d.Params[1]
	if p := d.Tail(far); p > 0.01 {
		t.Fatalf("tail far from mean should be tiny, got %.4f", p)
	}
}

// Exponential is used as a fit to positive metric data whose normal range sits
// around the mean (1/rate). C3 regression: a collapse toward zero (an outage) is
// anomalous — the two-sided tail must flag it, consistent with lognormal/normal.
// A one-sided (high) detector suppresses the low direction upstream, not here.
func TestExponentialTailTwoSided(t *testing.T) {
	d := Distribution{Family: "exponential", Params: []float64{1}} // mean 1
	if p := d.Tail(0); p > 0.05 {
		t.Fatalf("x=0 (an outage) must be extreme for a metric, got %.3f", p)
	}
	if p := d.Tail(1); p < 0.5 {
		t.Fatalf("x at the mean should not be extreme, got %.3f", p)
	}
	if p := d.Tail(10); p > 0.01 {
		t.Fatalf("large x must be extreme, got %.4f", p)
	}
}

// A clearly right-skewed positive sample selects a skewed family (not normal).
func TestFitSkewedPrefersNonNormal(t *testing.T) {
	// Mostly small positive values with a long right tail.
	x := []float64{1, 1, 2, 1, 3, 2, 1, 1, 2, 1, 1, 40, 1, 2, 1, 1, 3, 1, 2, 1}
	d := NewGoProvider().FitDistribution(x)
	if d.Family == "normal" {
		t.Fatalf("expected a skewed family for right-skewed data, got normal")
	}
}
