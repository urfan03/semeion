package stats

import (
	"math"
	"testing"
)

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestMedian(t *testing.T) {
	cases := []struct {
		in   []float64
		want float64
	}{
		{nil, 0},
		{[]float64{5}, 5},
		{[]float64{3, 1, 2}, 2},      // odd
		{[]float64{4, 1, 3, 2}, 2.5}, // even → mean of middle two
		{[]float64{-1, -3, -2}, -2},  // negatives
	}
	for _, c := range cases {
		if got := Median(c.in); got != c.want {
			t.Errorf("Median(%v)=%v want %v", c.in, got, c.want)
		}
	}
	// Must not mutate the input.
	in := []float64{3, 1, 2}
	_ = Median(in)
	if in[0] != 3 || in[1] != 1 || in[2] != 2 {
		t.Errorf("Median mutated its input: %v", in)
	}
}

func TestMAD(t *testing.T) {
	// 40 at 100, 5 at 900: >half at the median → MAD is 0 (the robustness edge
	// the engine's fallback handles).
	xs := make([]float64, 0, 45)
	for i := 0; i < 40; i++ {
		xs = append(xs, 100)
	}
	for i := 0; i < 5; i++ {
		xs = append(xs, 900)
	}
	med, mad := MAD(xs)
	if med != 100 || mad != 0 {
		t.Fatalf("MAD: med=%v mad=%v want 100,0", med, mad)
	}
	// A symmetric spread: |dev| = {2,1,0,1,2} → MAD = 1.
	if _, m := MAD([]float64{1, 2, 3, 4, 5}); m != 1 {
		t.Errorf("MAD of 1..5 should be 1, got %v", m)
	}
	if _, m := MAD(nil); m != 0 {
		t.Errorf("MAD(nil) should be 0, got %v", m)
	}
}

func TestModifiedZScore(t *testing.T) {
	// MAD==0 → 0 (caller handles the flat-baseline fallback).
	if z := ModifiedZScore(500, 100, 0); z != 0 {
		t.Errorf("MAD==0 must return 0, got %v", z)
	}
	// 0.6745·(x−med)/mad.
	if z := ModifiedZScore(110, 100, 5); !approx(z, 0.6745*10/5, 1e-9) {
		t.Errorf("modified z wrong: %v", z)
	}
	// Symmetric in direction.
	if lo, hi := ModifiedZScore(90, 100, 5), ModifiedZScore(110, 100, 5); !approx(lo, -hi, 1e-9) {
		t.Errorf("z should be symmetric, got %v vs %v", lo, hi)
	}
}

func TestUpperTail(t *testing.T) {
	if !approx(UpperTail(0), 0.5, 1e-9) {
		t.Errorf("UpperTail(0) should be 0.5, got %v", UpperTail(0))
	}
	if !approx(UpperTail(1.6449), 0.05, 1e-3) { // 95th percentile
		t.Errorf("UpperTail(1.645) should be ~0.05, got %v", UpperTail(1.6449))
	}
	if !approx(UpperTail(1.96), 0.025, 1e-3) {
		t.Errorf("UpperTail(1.96) should be ~0.025, got %v", UpperTail(1.96))
	}
	// Monotone decreasing.
	if UpperTail(1) <= UpperTail(2) {
		t.Error("UpperTail must decrease as z grows")
	}
}

func TestMeanStd(t *testing.T) {
	mean, std := MeanStd([]float64{2, 4, 4, 4, 5, 5, 7, 9})
	if !approx(mean, 5, 1e-9) {
		t.Errorf("mean: %v", mean)
	}
	if !approx(std, 2, 1e-9) { // population std
		t.Errorf("std: %v want 2", std)
	}
	if m, s := MeanStd(nil); m != 0 || s != 0 {
		t.Errorf("MeanStd(nil) should be 0,0 got %v,%v", m, s)
	}
}
