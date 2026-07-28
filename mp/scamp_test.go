package mp

import (
	"math"
	"math/rand/v2"
	"testing"
)

func randomSeries(n int, seed uint64) []float64 {
	rng := rand.New(rand.NewPCG(seed, seed^0xabcdef))
	out := make([]float64, n)
	for i := range out {
		out[i] = math.Sin(float64(i)*0.17) + 0.3*rng.NormFloat64()
	}
	return out
}

func TestParallelMatchesSTOMP(t *testing.T) {
	for _, tc := range []struct {
		n, m, workers int
	}{
		{400, 16, 1},
		{400, 16, 4},
		{733, 31, 3},
		{1000, 64, 8},
	} {
		ts := randomSeries(tc.n, uint64(tc.n))
		want := stomp(ts, tc.m, false)
		got := scamp(ts, tc.m, false, tc.workers)
		if len(got) != len(want) {
			t.Fatalf("n=%d m=%d: length %d vs %d", tc.n, tc.m, len(got), len(want))
		}
		for i := range want {
			if math.Abs(got[i]-want[i]) > 1e-6 {
				t.Fatalf("n=%d m=%d workers=%d: index %d got %v want %v", tc.n, tc.m, tc.workers, i, got[i], want[i])
			}
		}
	}
}

func TestParallelMatchesSTOMPWithFlatWindows(t *testing.T) {
	ts := make([]float64, 600)
	for i := range ts {
		ts[i] = 7
	}
	for i := 300; i < 330; i++ {
		ts[i] = 7 + math.Cos(float64(i)*0.9)
	}
	want := stomp(ts, 30, true)
	got := scamp(ts, 30, true, 4)
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Fatalf("flat handling diverged at %d: %v vs %v", i, got[i], want[i])
		}
	}
}

func TestParallelDegenerateInputs(t *testing.T) {
	if Parallel([]float64{1, 2, 3}, 8, 2) != nil {
		t.Fatal("a series shorter than 2m has no profile")
	}
	if Parallel(randomSeries(100, 1), 1, 2) != nil {
		t.Fatal("a window below 2 has no profile")
	}
	if got := Parallel(randomSeries(200, 2), 20, 0); len(got) != 181 {
		t.Fatalf("workers=0 must fall back to GOMAXPROCS, got %d values", len(got))
	}
}

func BenchmarkProfileSerial(b *testing.B) {
	ts := randomSeries(4000, 9)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stomp(ts, 8, true)
	}
}

func BenchmarkProfileParallel(b *testing.B) {
	ts := randomSeries(4000, 9)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scamp(ts, 8, true, 0)
	}
}
