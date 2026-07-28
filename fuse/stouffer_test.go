package fuse

import (
	"math"
	"math/rand/v2"
	"sort"
	"testing"
)

func TestNormalQuantileRoundTrips(t *testing.T) {
	for _, p := range []float64{1e-9, 1e-4, 0.01, 0.025, 0.1, 0.5, 0.9, 0.975, 0.999, 1 - 1e-9} {
		x := normalQuantile(p)
		if back := normalCDF(x); math.Abs(back-p) > 1e-9 {
			t.Fatalf("p=%v: quantile %v maps back to %v", p, x, back)
		}
	}
	if math.Abs(normalQuantile(0.975)-1.959963985) > 1e-6 {
		t.Fatalf("the 97.5%% point should be 1.95996, got %v", normalQuantile(0.975))
	}
	if !math.IsInf(normalQuantile(0), -1) || !math.IsInf(normalQuantile(1), 1) {
		t.Fatal("the endpoints must be infinite")
	}
}

func TestStoufferCombinesAndWeights(t *testing.T) {
	if p := Stouffer([]float64{0.5, 0.5}, nil); math.Abs(p-0.5) > 1e-9 {
		t.Fatalf("two neutral p-values must stay neutral, got %v", p)
	}
	single := Stouffer([]float64{0.02}, nil)
	agree := Stouffer([]float64{0.02, 0.02, 0.02}, nil)
	if agree >= single {
		t.Fatalf("agreement must strengthen the evidence: %v vs %v", agree, single)
	}

	trusted := Stouffer([]float64{0.001, 0.9}, []float64{5, 1})
	ignored := Stouffer([]float64{0.001, 0.9}, []float64{1, 5})
	if trusted >= ignored {
		t.Fatalf("weighting the informative detector must lower the combined p: %v vs %v", trusted, ignored)
	}
	if p := Stouffer(nil, nil); p != 1 {
		t.Fatalf("no inputs must give p=1, got %v", p)
	}
	if p := Stouffer([]float64{0.1, 0.1}, []float64{0, 0}); p != 1 {
		t.Fatalf("zero weights must give p=1, got %v", p)
	}
	if p := Stouffer([]float64{0}, nil); p <= 0 || p > 1e-30 {
		t.Fatalf("a zero p-value must clamp to a tiny positive value, got %v", p)
	}
}

func TestStoufferUniformUnderNull(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 7))
	below := 0
	const n = 20000
	for i := 0; i < n; i++ {
		if Stouffer([]float64{rng.Float64(), rng.Float64(), rng.Float64()}, nil) < 0.05 {
			below++
		}
	}
	rate := float64(below) / n
	if math.Abs(rate-0.05) > 0.01 {
		t.Fatalf("false-positive rate under the null should be ~5%%, got %.3f", rate)
	}
}

func syntheticPanel(n int, seed uint64) [][]float64 {
	rng := rand.New(rand.NewPCG(seed, seed^0x1234))
	informative := 3
	streams := make([][]float64, informative+2)
	for i := range streams {
		streams[i] = make([]float64, n)
	}
	for i := 0; i < n; i++ {
		anomaly := i%100 == 0
		for d := 0; d < informative; d++ {
			p := 0.5
			if anomaly && rng.Float64() < 0.8 {
				p = 0.0005
			} else if rng.Float64() < 0.03 {
				p = 0.0005
			}
			streams[d][i] = p
		}
		streams[informative][i] = 0.5
		if rng.Float64() < 0.25 {
			streams[informative][i] = 0.0005
		}
		streams[informative+1][i] = 0.5
	}
	return streams
}

func TestReliabilityDownweightsNoiseAndSilence(t *testing.T) {
	streams := syntheticPanel(4000, 9)
	rel := NewReliability(len(streams), 0.999, 0.05)
	buf := make([]float64, len(streams))
	for i := 0; i < len(streams[0]); i++ {
		for j := range streams {
			buf[j] = streams[j][i]
		}
		rel.Observe(buf)
	}
	w := rel.Weights()
	noisy, silent := len(streams)-2, len(streams)-1
	for d := 0; d < noisy; d++ {
		if w[d] <= w[noisy] {
			t.Fatalf("informative detector %d must outweigh the noisy one: %v vs %v", d, w[d], w[noisy])
		}
		if w[d] <= w[silent] {
			t.Fatalf("informative detector %d must outweigh the silent one: %v vs %v", d, w[d], w[silent])
		}
	}
	if rel.Rate(noisy) < 0.15 {
		t.Fatalf("the noisy detector should show a high firing rate, got %v", rel.Rate(noisy))
	}
	if rel.Rate(silent) != 0 {
		t.Fatalf("the silent detector never fires, got rate %v", rel.Rate(silent))
	}
	var sum float64
	for _, v := range w {
		sum += v
	}
	if math.Abs(sum-float64(len(w))) > 1e-9 {
		t.Fatalf("weights must be normalised to the detector count, got %v", sum)
	}
}

func TestReliabilityFallsBackToUniform(t *testing.T) {
	rel := NewReliability(3, 0.99, 0.05)
	for i := 0; i < 100; i++ {
		rel.Observe([]float64{0.5, 0.5, 0.5})
	}
	for i, v := range rel.Weights() {
		if v != 1 {
			t.Fatalf("with nothing to learn the weights must stay uniform, got %v at %d", v, i)
		}
	}
}

func TestWeightedCombineBeatsUnweightedOnNoise(t *testing.T) {
	streams := syntheticPanel(4000, 21)
	n := len(streams[0])

	weighted, weights := WeightedCombine(streams, WeightedOptions{Warmup: 300})
	if len(weighted) != n || len(weights) != len(streams) {
		t.Fatalf("unexpected shapes: %d %d", len(weighted), len(weights))
	}
	noisy := len(streams) - 2
	for d := 0; d < noisy; d++ {
		if weights[d] <= weights[noisy] {
			t.Fatalf("detector %d should outrank the noisy stream: %v vs %v", d, weights[d], weights[noisy])
		}
	}

	buf := make([]float64, len(streams))
	plain := make([]float64, n)
	for i := 0; i < n; i++ {
		for j := range streams {
			buf[j] = streams[j][i]
		}
		plain[i] = Stouffer(buf, nil)
	}

	events := 0
	for i := 300; i < n; i++ {
		if i%100 == 0 {
			events++
		}
	}
	hitsAtBudget := func(p []float64) int {
		idx := make([]int, 0, n-300)
		for i := 300; i < n; i++ {
			idx = append(idx, i)
		}
		sort.SliceStable(idx, func(a, b int) bool { return p[idx[a]] < p[idx[b]] })
		hits := 0
		for _, i := range idx[:events] {
			if i%100 == 0 {
				hits++
			}
		}
		return hits
	}
	wHits, pHits := hitsAtBudget(weighted), hitsAtBudget(plain)
	if wHits <= pHits {
		t.Fatalf("at an equal alarm budget, weighting must find more real events: %d vs %d of %d", wHits, pHits, events)
	}

	if c, w := WeightedCombine(nil, WeightedOptions{}); c != nil || w != nil {
		t.Fatal("no streams must combine to nil")
	}
}
