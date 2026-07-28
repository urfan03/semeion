package prep

import (
	"math"
	"math/rand/v2"
	"testing"
)

func seasonalSeries(n, period int, amp, noise float64, seed uint64) []float64 {
	rng := rand.New(rand.NewPCG(seed, seed^0x77))
	out := make([]float64, n)
	for i := range out {
		out[i] = 100 + amp*math.Sin(2*math.Pi*float64(i)/float64(period)) + noise*rng.NormFloat64()
	}
	return out
}

func TestDetectPeriodFindsTheCycle(t *testing.T) {
	for _, period := range []int{24, 48, 144} {
		vals := seasonalSeries(period*12, period, 10, 0.5, uint64(period))
		got, strength := DetectPeriod(vals, Options{})
		if math.Abs(float64(got-period)) > float64(period)/10 {
			t.Fatalf("period %d: detected %d (strength %.3f)", period, got, strength)
		}
		if strength < 0.5 {
			t.Fatalf("a clean sine should look strongly seasonal, got %.3f", strength)
		}
	}

	rng := rand.New(rand.NewPCG(3, 5))
	noise := make([]float64, 2000)
	for i := range noise {
		noise[i] = rng.NormFloat64()
	}
	if _, strength := DetectPeriod(noise, Options{}); strength > 0.2 {
		t.Fatalf("white noise must not look seasonal, got %.3f", strength)
	}
	if p, _ := DetectPeriod([]float64{1, 2, 3}, Options{}); p != 0 {
		t.Fatal("too little data must give no period")
	}
	flat := make([]float64, 500)
	if p, _ := DetectPeriod(flat, Options{}); p != 0 {
		t.Fatal("a constant series has no period")
	}
}

func TestDeseasonalizeRemovesTheCycle(t *testing.T) {
	period := 48
	vals := seasonalSeries(period*15, period, 12, 0.4, 9)
	resid, got := Deseasonalize(vals, Options{})
	if got == 0 {
		t.Fatal("a strongly seasonal series must be deseasonalized")
	}
	if len(resid) != len(vals) {
		t.Fatalf("length must be preserved: %d vs %d", len(resid), len(vals))
	}

	spread := func(xs []float64) float64 {
		var sum float64
		for _, v := range xs {
			sum += v
		}
		mean := sum / float64(len(xs))
		var ss float64
		for _, v := range xs {
			ss += (v - mean) * (v - mean)
		}
		return math.Sqrt(ss / float64(len(xs)))
	}
	if spread(resid) >= spread(vals)/3 {
		t.Fatalf("the residual must be far quieter than the raw series: %.3f vs %.3f", spread(resid), spread(vals))
	}
	if _, s := DetectPeriod(resid, Options{}); s > 0.5 {
		t.Fatalf("the residual must not still look seasonal, got %.3f", s)
	}
}

func TestDeseasonalizeKeepsTheAnomaly(t *testing.T) {
	period := 24
	vals := seasonalSeries(period*30, period, 10, 0.3, 11)
	at := len(vals) - 100
	vals[at] += 25

	resid, got := Deseasonalize(vals, Options{})
	if got == 0 {
		t.Fatal("expected deseasonalization to engage")
	}
	var elsewhere float64
	for i, v := range resid {
		if i != at && math.Abs(v) > elsewhere {
			elsewhere = math.Abs(v)
		}
	}
	if math.Abs(resid[at]) <= elsewhere {
		t.Fatalf("the injected spike must dominate the residual: %.3f vs %.3f", resid[at], elsewhere)
	}
}

func TestDeseasonalizePassesThroughWhenNotSeasonal(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 17))
	vals := make([]float64, 1500)
	for i := range vals {
		vals[i] = rng.NormFloat64()
	}
	out, period := Deseasonalize(vals, Options{})
	if period != 0 {
		t.Fatalf("white noise must not be deseasonalized, got period %d", period)
	}
	for i := range vals {
		if out[i] != vals[i] {
			t.Fatalf("pass-through must copy the input exactly, diverged at %d", i)
		}
	}
	short, p := Deseasonalize([]float64{1, 2, 3}, Options{Period: 48})
	if p != 0 || len(short) != 3 {
		t.Fatal("a series shorter than the period must pass through")
	}
}

func TestCausalDeseasonalizeUsesOnlyThePast(t *testing.T) {
	period := 24
	vals := seasonalSeries(period*40, period, 8, 0.3, 21)
	full, p := Deseasonalize(vals, Options{Period: period})
	if p != period {
		t.Fatalf("an explicit period must be honoured, got %d", p)
	}
	cut := len(vals) / 2
	prefix, _ := Deseasonalize(vals[:cut], Options{Period: period})
	for i := 0; i < cut; i++ {
		if math.Abs(full[i]-prefix[i]) > 1e-9 {
			t.Fatalf("future data changed a past residual at %d: %v vs %v", i, full[i], prefix[i])
		}
	}
	var late float64
	for i := cut; i < len(full); i++ {
		late += math.Abs(full[i])
	}
	if late/float64(len(full)-cut) > 1 {
		t.Fatalf("the causal residual should settle near zero, mean |resid| = %.3f", late/float64(len(full)-cut))
	}
}

func TestAbs(t *testing.T) {
	got := Abs([]float64{-1, 2, -3})
	want := []float64{1, 2, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Abs wrong: %v", got)
		}
	}
}
