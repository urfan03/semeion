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

	if got[0] < 23 || got[0] > 25 {
		t.Fatalf("period: got %d, want ~24", got[0])
	}
}

func TestDetectSeasonalityNoneOnNoise(t *testing.T) {

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

	for h, v := range f {
		if v < 40 || v > 160 {
			t.Fatalf("forecast[%d]=%.1f out of band [40,160]", h, v)
		}
	}
}

func TestChangePointsTrendHasNone(t *testing.T) {
	x := make([]float64, 200)
	for i := range x {
		x[i] = float64(i)
	}
	if cps := NewGoProvider().ChangePoints(x); len(cps) != 0 {
		t.Fatalf("a pure ramp must yield no change points, got %v", cps)
	}
}

func TestChangePointsStepOnTrend(t *testing.T) {
	x := make([]float64, 80)
	for i := range x {
		x[i] = float64(i)
		if i >= 40 {
			x[i] += 100
		}
	}
	cps := NewGoProvider().ChangePoints(x)
	if len(cps) == 0 {
		t.Fatal("a step on a trend should still be detected")
	}
	if cps[0] < 35 || cps[0] > 45 {
		t.Fatalf("change point near 40 expected, got %d", cps[0])
	}
}

func TestDetectSeasonalityUnderTrend(t *testing.T) {
	period := 24
	x := make([]float64, 240)
	for i := range x {
		x[i] = 100 + 30*sineAt(i, period) + 2*float64(i)
	}
	got := NewGoProvider().DetectSeasonality(x)
	if len(got) == 0 {
		t.Fatal("seasonality under a trend must still be detected")
	}
	if got[0] < period-2 || got[0] > period+2 {
		t.Fatalf("expected period ~%d, got %d", period, got[0])
	}
}

func sineAt(i, period int) float64 { return math.Sin(2 * math.Pi * float64(i) / float64(period)) }

func TestForecastBands(t *testing.T) {
	x := sineSeries(120, 12)
	bands := NewGoProvider().ForecastBands(x, 6)
	if len(bands) != 6 {
		t.Fatalf("expected 6 bands, got %d", len(bands))
	}
	for _, b := range bands {
		if b.Lower > b.Point || b.Point > b.Upper {
			t.Fatalf("band must bracket the point: %+v", b)
		}
	}
	w0 := bands[0].Upper - bands[0].Lower
	wN := bands[5].Upper - bands[5].Lower
	if wN < w0 {
		t.Errorf("interval should widen with horizon: first=%.2f last=%.2f", w0, wN)
	}
}
