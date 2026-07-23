package model

import (
	"math"
	"testing"
)

func TestHoltWintersForecastAccuracy(t *testing.T) {
	period := 12
	gen := func(i int) float64 {
		return 100 + 0.5*float64(i) + 20*math.Sin(2*math.Pi*float64(i%period)/float64(period))
	}
	n := 240
	train := make([]float64, n)
	for i := 0; i < n; i++ {
		train[i] = gen(i)
	}
	horizon := 24
	fc := holtWinters(train, period, horizon)
	if fc == nil {
		t.Fatal("holtWinters returned nil on a valid seasonal+trend series")
	}
	var mae float64
	for h := 0; h < horizon; h++ {
		mae += math.Abs(fc[h] - gen(n+h))
	}
	mae /= float64(horizon)
	if mae > 5 {
		t.Fatalf("Holt-Winters MAE should track a clean seasonal+trend series closely, got %.2f", mae)
	}
}

func TestHoltWintersBeatsSeasonalNaive(t *testing.T) {
	period := 24
	gen := func(i int) float64 {
		return 200 + 1.2*float64(i) + 40*math.Sin(2*math.Pi*float64(i%period)/float64(period))
	}
	n := 240
	train := make([]float64, n)
	for i := 0; i < n; i++ {
		train[i] = gen(i)
	}
	horizon := 24

	hw := holtWinters(train, period, horizon)

	d := decompose(train, period)
	slope, intercept := linFit(d.Trend)
	naive := make([]float64, horizon)
	for h := 0; h < horizon; h++ {
		j := n + h
		naive[h] = intercept + slope*float64(j) + d.Seasonal[j%period]
	}

	mae := func(fc []float64) float64 {
		var s float64
		for h := 0; h < horizon; h++ {
			s += math.Abs(fc[h] - gen(n+h))
		}
		return s / float64(horizon)
	}
	if mae(hw) > mae(naive)+1e-9 {
		t.Fatalf("Holt-Winters (%.2f) should be at least as accurate as seasonal-naive (%.2f) on a trending seasonal series", mae(hw), mae(naive))
	}
}

func TestForecastBandsWidenWithHorizon(t *testing.T) {
	x := make([]float64, 100)
	for i := range x {
		x[i] = 100 + 5*math.Sin(float64(i)*0.3)
	}
	bands := NewGoProvider().ForecastBands(x, 20)
	w0 := bands[0].Upper - bands[0].Lower
	wN := bands[19].Upper - bands[19].Lower
	if w0 <= 0 {
		t.Fatalf("band should have positive width, got %v", w0)
	}
	if wN <= w0*2 {
		t.Fatalf("multi-step band must widen with horizon (~sqrt(h)); w0=%.3f w19=%.3f", w0, wN)
	}
}
