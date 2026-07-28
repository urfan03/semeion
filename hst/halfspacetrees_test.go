package hst

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestForestFlagsShiftedPoints(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	f := New(2, Options{Trees: 20, Height: 8, WindowSize: 200, Seed: 3})
	for i := 0; i < 2000; i++ {
		f.Update([]float64{0.5 + 0.02*rng.NormFloat64(), 0.5 + 0.02*rng.NormFloat64()})
	}
	if !f.Warm() {
		t.Fatal("forest should be warm after 2000 points")
	}
	normal := f.Score([]float64{0.5, 0.5})
	outlier := f.Score([]float64{0.02, 0.97})
	if outlier <= normal {
		t.Fatalf("outlier must score above the dense region: outlier=%.4f normal=%.4f", outlier, normal)
	}
	if normal < 0 || normal > 1 || outlier < 0 || outlier > 1 {
		t.Fatalf("scores must stay in [0,1]: %.4f %.4f", normal, outlier)
	}
}

func TestForestColdStartIsZero(t *testing.T) {
	f := New(1, Options{WindowSize: 50})
	for i := 0; i < 49; i++ {
		if s := f.Update([]float64{0.4}); s != 0 {
			t.Fatalf("score before the first window closes must be 0, got %v at %d", s, i)
		}
	}
}

func TestForestDeterministic(t *testing.T) {
	run := func() []float64 {
		f := New(2, Options{Trees: 8, Height: 6, WindowSize: 64, Seed: 42})
		rng := rand.New(rand.NewPCG(1, 2))
		out := make([]float64, 300)
		for i := range out {
			out[i] = f.Update([]float64{rng.Float64(), rng.Float64()})
		}
		return out
	}
	a, b := run(), run()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("same seed must give identical scores, diverged at %d: %v vs %v", i, a[i], b[i])
		}
	}
}

func TestScalerMapsToUnit(t *testing.T) {
	s := NewScaler(2)
	for _, v := range [][]float64{{10, -5}, {20, 5}, {15, 0}} {
		out := s.Transform(v)
		for i, o := range out {
			if o < 0 || o > 1 {
				t.Fatalf("scaled feature %d out of range: %v", i, o)
			}
		}
	}
	out := s.Transform([]float64{20, -5})
	if math.Abs(out[0]-1) > 1e-9 || math.Abs(out[1]) > 1e-9 {
		t.Fatalf("extremes should map to 1 and 0, got %v", out)
	}
}

func TestSeriesRaisesOnLevelShift(t *testing.T) {
	const n, at, width = 2000, 1400, 40
	vals := make([]float64, n)
	rng := rand.New(rand.NewPCG(5, 9))
	for i := range vals {
		vals[i] = 10 + math.Sin(float64(i)*0.15) + 0.05*rng.NormFloat64()
	}
	for i := at; i < at+width; i++ {
		vals[i] += 12
	}
	scores := Series(vals, SeriesOptions{Options: Options{Trees: 25, Height: 8, WindowSize: 250, Seed: 17}, Lags: 3, Diff: true})
	if len(scores) != n {
		t.Fatalf("expected %d scores, got %d", n, len(scores))
	}
	var inAnomaly, elsewhere float64
	cnt := 0
	for i, s := range scores {
		if i >= at && i < at+width {
			if s > inAnomaly {
				inAnomaly = s
			}
		} else if i > 500 {
			elsewhere += s
			cnt++
		}
	}
	if cnt == 0 || inAnomaly <= elsewhere/float64(cnt) {
		t.Fatalf("anomaly window should score above the baseline mean: %.4f vs %.4f", inAnomaly, elsewhere/float64(cnt))
	}
}
