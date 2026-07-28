package model

import (
	"math"
	"testing"
)

func TestSTLRecoversTrendAndSeasonal(t *testing.T) {
	const p = 12
	const n = 120
	season := make([]float64, p)
	for k := 0; k < p; k++ {
		season[k] = 5 * math.Sin(2*math.Pi*float64(k)/float64(p))
	}
	trueTrend := func(i int) float64 { return 10 + 0.2*float64(i) }
	x := make([]float64, n)
	for i := 0; i < n; i++ {
		x[i] = trueTrend(i) + season[i%p]
	}

	d := stl(x, p, 2, 2)

	for i := 0; i < n; i++ {
		if math.IsNaN(d.Trend[i]) || math.IsInf(d.Trend[i], 0) ||
			math.IsNaN(d.Seasonal[i]) || math.IsInf(d.Seasonal[i], 0) ||
			math.IsNaN(d.Resid[i]) || math.IsInf(d.Resid[i], 0) {
			t.Fatalf("STL components must be finite at %d: trend=%v seasonal=%v resid=%v", i, d.Trend[i], d.Seasonal[i], d.Resid[i])
		}
		if math.Abs(d.Trend[i]+d.Seasonal[i]+d.Resid[i]-x[i]) > 1e-9 {
			t.Fatalf("reconstruction must be exact at %d", i)
		}
	}

	var rss float64
	for _, r := range d.Resid {
		rss += r * r
	}
	if rmse := math.Sqrt(rss / n); rmse > 1.5 {
		t.Fatalf("STL residual too large for a clean signal: rmse=%.3f", rmse)
	}

	rise := d.Trend[n-1] - d.Trend[0]
	if rise < 18 || rise > 30 {
		t.Fatalf("STL trend must capture the ~23.8 rise, got %.2f", rise)
	}

	var smean float64
	for _, sv := range d.Seasonal {
		smean += sv
	}
	smean /= n
	if math.Abs(smean) > 0.5 {
		t.Fatalf("STL seasonal must be roughly zero-mean, got %.3f", smean)
	}

	naive := classicalDecompose(x, p)
	stlEnd := math.Abs(d.Trend[n-1] - trueTrend(n-1))
	naiveEnd := math.Abs(naive.Trend[n-1] - trueTrend(n-1))
	if stlEnd > naiveEnd+1e-9 {
		t.Fatalf("STL trend at the series end (err %.3f) should track at least as well as the naive centered average (err %.3f)", stlEnd, naiveEnd)
	}
}
