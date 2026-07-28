package fuse

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestCauchyIsUniformUnderTheNull(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	const n = 40000
	below := 0
	for i := 0; i < n; i++ {
		p := []float64{rng.Float64(), rng.Float64(), rng.Float64()}
		if Cauchy(p, nil) < 0.05 {
			below++
		}
	}
	if rate := float64(below) / n; math.Abs(rate-0.05) > 0.006 {
		t.Fatalf("independent nulls should give ~5%%, got %.4f", rate)
	}
}

func perfectlyDependent(rng *rand.Rand, m int) []float64 {
	shared := rng.Float64()
	out := make([]float64, m)
	for i := range out {
		out[i] = shared
	}
	return out
}

func TestCauchySurvivesPerfectDependence(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 5))
	const n = 40000
	cauchyBelow, fisherBelow, agreeBelow := 0, 0, 0
	for i := 0; i < n; i++ {
		p := perfectlyDependent(rng, 5)
		if Cauchy(p, nil) < 0.05 {
			cauchyBelow++
		}
		if Fisher(p) < 0.05 {
			fisherBelow++
		}
		if Agree(p, 3) < 0.05 {
			agreeBelow++
		}
	}
	cauchy := float64(cauchyBelow) / n
	fisher := float64(fisherBelow) / n
	agree := float64(agreeBelow) / n

	if cauchy > 0.075 {
		t.Fatalf("Cauchy must stay near its nominal level under total dependence, got %.4f", cauchy)
	}
	if fisher <= 0.075 {
		t.Fatalf("Fisher is expected to break under total dependence; got %.4f, so this test proves nothing", fisher)
	}
	if agree <= 0.075 {
		t.Fatalf("order-statistic agreement is expected to break too; got %.4f", agree)
	}
	t.Logf("false-positive rate at nominal 5%% under perfect dependence: cauchy=%.4f fisher=%.4f agree=%.4f",
		cauchy, fisher, agree)
}

func TestCauchyDetectsRealSignal(t *testing.T) {
	strong := Cauchy([]float64{1e-6, 0.5, 0.5}, nil)
	if strong > 0.01 {
		t.Fatalf("one very strong detector must move the combination, got %v", strong)
	}
	all := Cauchy([]float64{1e-6, 1e-6, 1e-6}, nil)
	if all >= strong {
		t.Fatalf("agreement must strengthen it further: %v vs %v", all, strong)
	}
	neutral := Cauchy([]float64{0.5, 0.5, 0.5}, nil)
	if math.Abs(neutral-0.5) > 1e-9 {
		t.Fatalf("neutral inputs must stay neutral, got %v", neutral)
	}
	if Cauchy(nil, nil) != 1 {
		t.Fatal("no inputs must give p=1")
	}
	if Cauchy([]float64{0.1, 0.1}, []float64{0, 0}) != 1 {
		t.Fatal("zero weights must give p=1")
	}
	if p := Cauchy([]float64{0}, nil); p <= 0 || p > 1e-15 {
		t.Fatalf("a zero p-value must clamp to something tiny and positive, got %v", p)
	}
}

func TestCauchyWeighting(t *testing.T) {
	trusted := Cauchy([]float64{1e-4, 0.9}, []float64{5, 1})
	ignored := Cauchy([]float64{1e-4, 0.9}, []float64{1, 5})
	if trusted >= ignored {
		t.Fatalf("weighting the informative detector must lower the combination: %v vs %v", trusted, ignored)
	}
}

func TestCauchyStreams(t *testing.T) {
	a := []float64{0.5, 1e-6, 0.5, 0.5}
	b := []float64{0.5, 1e-6, 0.5}
	out := CauchyStreams([][]float64{a, b}, nil)
	if len(out) != 3 {
		t.Fatalf("output must match the shortest stream, got %d", len(out))
	}
	if out[1] >= out[0] {
		t.Fatalf("the joint event must combine lower: %v vs %v", out[1], out[0])
	}
	if CauchyStreams(nil, nil) != nil {
		t.Fatal("no streams must give nil")
	}
}

func TestHarmonicMean(t *testing.T) {
	if p := HarmonicMean([]float64{0.5, 0.5}); math.Abs(p-0.5) > 1e-12 {
		t.Fatalf("identical p-values must give themselves back, got %v", p)
	}
	mixed := HarmonicMean([]float64{1e-6, 0.9})
	if mixed > 1e-5 {
		t.Fatalf("the harmonic mean is dominated by the smallest p-value, got %v", mixed)
	}
	if HarmonicMean(nil) != 1 {
		t.Fatal("no inputs must give p=1")
	}
	if p := HarmonicMean([]float64{0}); p > 1e-299 {
		t.Fatalf("a zero p-value must clamp, got %v", p)
	}
}
