package model

import "testing"

func TestFitNormalAndTail(t *testing.T) {
	x := []float64{-3, -2, -1, 0, 1, 2, 3, -2.5, 2.5, -1.5, 1.5, 0.5, -0.5}
	d := NewGoProvider().FitDistribution(x)
	if d.Family != "normal" {
		t.Fatalf("family: got %q, want normal", d.Family)
	}
	if p := d.Tail(d.Params[0], "both"); p < 0.5 {
		t.Fatalf("tail at mean should be high, got %.3f", p)
	}
	far := d.Params[0] + 8*d.Params[1]
	if p := d.Tail(far, "both"); p > 0.01 {
		t.Fatalf("tail far from mean should be tiny, got %.4f", p)
	}
}

func TestExponentialTailSideAware(t *testing.T) {
	d := Distribution{Family: "exponential", Params: []float64{1}}
	if p := d.Tail(0, "both"); p < 0.5 {
		t.Fatalf("x=0 is the exponential mode; must not be extreme under both, got %.3f", p)
	}
	if p := d.Tail(10, "both"); p > 0.01 {
		t.Fatalf("large x must be extreme, got %.4f", p)
	}
	if p := d.Tail(0, "low"); p > 0.05 {
		t.Fatalf("x=0 must be extreme when SideLow (outage), got %.3f", p)
	}
	if p := d.Tail(1, "high"); p < 0.3 {
		t.Fatalf("x at the mean should not be extreme on the high side, got %.3f", p)
	}
}

func TestFitSkewedPrefersNonNormal(t *testing.T) {

	x := []float64{1, 1, 2, 1, 3, 2, 1, 1, 2, 1, 1, 40, 1, 2, 1, 1, 3, 1, 2, 1}
	d := NewGoProvider().FitDistribution(x)
	if d.Family == "normal" {
		t.Fatalf("expected a skewed family for right-skewed data, got normal")
	}
}
