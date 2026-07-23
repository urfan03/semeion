package model

import "testing"

// #6: a series with a clear sustained level shift is split into two regimes and
// flagged as a genuine regime shift with high probability.
func TestRegimeShiftOnStep(t *testing.T) {
	var s []float64
	for i := 0; i < 60; i++ {
		s = append(s, 100+float64(i%3)) // ~100
	}
	for i := 0; i < 60; i++ {
		s = append(s, 160+float64(i%3)) // stepped up to ~160
	}
	regs := Regimes(s)
	if len(regs) < 2 {
		t.Fatalf("a stepped series should yield >=2 regimes, got %d", len(regs))
	}
	shift := RegimeShift(s)
	if !shift.Shifted || shift.Probability < 0.9 {
		t.Fatalf("a sustained +60 step should read as a regime shift: %+v", shift)
	}
	if shift.FromMean > shift.ToMean {
		t.Fatalf("shift direction wrong: from=%v to=%v", shift.FromMean, shift.ToMean)
	}
}

// #6: a stationary series is one regime with no shift.
func TestRegimeNoShiftOnStationary(t *testing.T) {
	var s []float64
	for i := 0; i < 120; i++ {
		s = append(s, 100+float64(i%5)-2)
	}
	if regs := Regimes(s); len(regs) != 1 {
		t.Fatalf("a stationary series should be a single regime, got %d", len(regs))
	}
	if shift := RegimeShift(s); shift.Shifted {
		t.Fatalf("stationary series must not report a regime shift: %+v", shift)
	}
}
