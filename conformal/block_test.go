package conformal

import (
	"math"
	"math/rand/v2"
	"testing"
)

func autocorrelated(n int, rho float64, seed uint64) []float64 {
	rng := rand.New(rand.NewPCG(seed, seed^0x77))
	out := make([]float64, n)
	prev := 0.0
	for i := range out {
		prev = rho*prev + math.Sqrt(1-rho*rho)*rng.NormFloat64()
		out[i] = prev
	}
	return out
}

func TestSlidingMax(t *testing.T) {
	got := slidingMax([]float64{1, 5, 2, 8, 3}, 3)
	want := []float64{5, 8, 8}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
	if slidingMax([]float64{1, 2}, 5) != nil {
		t.Fatal("a window longer than the input has no maxima")
	}
	if got := slidingMax([]float64{1, math.NaN(), 3}, 2); len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("NaN must be skipped, got %v", got)
	}
}

func TestBlockWindowsCoverPowersOfTwo(t *testing.T) {
	got := blockWindows(20)
	want := []int{1, 2, 4, 8, 16, 20}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
	if got := blockWindows(1); len(got) != 1 || got[0] != 1 {
		t.Fatalf("a single-point window must give [1], got %v", got)
	}
}

func TestBlockPicksTheRightWindow(t *testing.T) {
	cal := autocorrelated(4000, 0, 1)
	b := NewBlock(cal, 32, 0.01, 0)
	if len(b.Windows()) == 0 {
		t.Fatal("no windows calibrated")
	}
	if b.Size(1) == 0 || b.Size(32) == 0 {
		t.Fatal("both ends must be calibrated")
	}
	if b.Threshold(1) >= b.Threshold(32) {
		t.Fatalf("a longer scan must need a higher bar: %v vs %v", b.Threshold(1), b.Threshold(32))
	}
	if b.P(0, 1) >= 1.01 {
		t.Fatal("a mid-distribution value must give a large p-value")
	}
}

func TestBlockHoldsTheRateUnderAutocorrelation(t *testing.T) {
	const rho, alpha = 0.9, 0.01
	cal := autocorrelated(20000, rho, 3)
	test := autocorrelated(20000, rho, 5)
	const runLen = 16

	point := NewTrimmed(cal, alpha, 0)
	block := NewBlock(cal, runLen, alpha, 0)

	maxes := slidingMax(test, runLen)
	pointFired, blockFired := 0, 0
	for _, m := range maxes {
		if point.Alarm(m) {
			pointFired++
		}
		if block.Alarm(m, runLen) {
			blockFired++
		}
	}
	pointRate := float64(pointFired) / float64(len(maxes))
	blockRate := float64(blockFired) / float64(len(maxes))

	if pointRate <= 3*alpha {
		t.Fatalf("a pointwise null is expected to be violated by a scan; got %.4f, so this test proves nothing", pointRate)
	}
	if blockRate > 2*alpha {
		t.Fatalf("block calibration must hold the rate for a scan of the same length: %.4f vs nominal %v", blockRate, alpha)
	}
	t.Logf("scan of %d points at nominal alpha=%v: pointwise=%.4f block=%.4f", runLen, alpha, pointRate, blockRate)
}

func TestBlockStillCatchesRealAnomalies(t *testing.T) {
	const rho, alpha, runLen = 0.9, 0.01, 16
	cal := autocorrelated(20000, rho, 7)
	block := NewBlock(cal, runLen, alpha, 0)
	if !block.Alarm(12, runLen) {
		t.Fatalf("a 12-sigma run maximum must still alarm, threshold was %v", block.Threshold(runLen))
	}
	if block.P(12, runLen) > alpha {
		t.Fatalf("its p-value must clear alpha, got %v", block.P(12, runLen))
	}
}

func TestBlockDegenerate(t *testing.T) {
	b := NewBlock(nil, 8, 0.01, 0)
	if len(b.Windows()) != 0 {
		t.Fatal("no calibration data means no windows")
	}
	if b.P(99, 4) != 1 || b.Alarm(99, 4) {
		t.Fatal("with nothing calibrated there is no evidence")
	}
	if !math.IsInf(b.Threshold(4), 1) {
		t.Fatal("and no reachable threshold")
	}
	if b.Size(4) != 0 {
		t.Fatal("and no calibration size")
	}
}
