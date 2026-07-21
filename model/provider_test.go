package model

import (
	"math"
	"testing"
)

func sineSeries(n, period int) []float64 {
	x := make([]float64, n)
	for i := 0; i < n; i++ {
		x[i] = 100 + 50*math.Sin(2*math.Pi*float64(i)/float64(period))
	}
	return x
}

func TestDetectSeasonality(t *testing.T) {
	x := sineSeries(240, 24)
	got := NewGoProvider().DetectSeasonality(x)
	if len(got) == 0 {
		t.Fatal("expected a period, got none")
	}
	// Allow ±1 sample of slack around the true period.
	if got[0] < 23 || got[0] > 25 {
		t.Fatalf("period: got %d, want ~24", got[0])
	}
}

func TestDetectSeasonalityNoneOnNoise(t *testing.T) {
	// Deterministic non-periodic ramp — no strong single period.
	x := make([]float64, 120)
	for i := range x {
		x[i] = float64(i)
	}
	if got := NewGoProvider().DetectSeasonality(x); len(got) != 0 {
		t.Fatalf("expected no period on a monotone ramp, got %v", got)
	}
}

func TestDecomposeReconstructs(t *testing.T) {
	x := sineSeries(120, 12)
	d := NewGoProvider().Decompose(x, 12)
	for i := range x {
		sum := d.Trend[i] + d.Seasonal[i] + d.Resid[i]
		if math.Abs(sum-x[i]) > 1e-9 {
			t.Fatalf("reconstruction at %d: %.6f vs %.6f", i, sum, x[i])
		}
	}
}

func TestChangePointsStep(t *testing.T) {
	x := make([]float64, 60)
	for i := range x {
		if i < 30 {
			x[i] = 100
		} else {
			x[i] = 200
		}
	}
	cps := NewGoProvider().ChangePoints(x)
	if len(cps) == 0 {
		t.Fatal("expected a change point at the step")
	}
	// The first detection should land shortly after the step at index 30.
	if cps[0] < 30 || cps[0] > 45 {
		t.Fatalf("change point: got %d, want ~30-45", cps[0])
	}
}

func TestForecastContinuesSeason(t *testing.T) {
	period := 12
	x := sineSeries(120, period)
	f := NewGoProvider().Forecast(x, period)
	if len(f) != period {
		t.Fatalf("forecast length: got %d", len(f))
	}
	// The forecast should stay within the series' amplitude band, not diverge.
	for h, v := range f {
		if v < 40 || v > 160 {
			t.Fatalf("forecast[%d]=%.1f out of band [40,160]", h, v)
		}
	}
}
