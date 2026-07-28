package fuse

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestFisherCombinesEvidence(t *testing.T) {
	if p := Fisher([]float64{0.5, 0.5}); p < 0.4 || p > 0.9 {
		t.Fatalf("two neutral p-values should stay unremarkable, got %v", p)
	}
	weak := Fisher([]float64{0.04})
	joint := Fisher([]float64{0.04, 0.04, 0.04})
	if joint >= weak {
		t.Fatalf("agreeing weak signals must combine into stronger evidence: %v vs %v", joint, weak)
	}
	if joint <= 0 || joint > 1 {
		t.Fatalf("combined p-value out of range: %v", joint)
	}
	if p := Fisher(nil); p != 1 {
		t.Fatalf("no inputs must yield p=1, got %v", p)
	}
	if p := Fisher([]float64{0}); p <= 0 || p > 1e-10 {
		t.Fatalf("a zero p-value must clamp, not blow up: %v", p)
	}
}

func TestFisherUniformUnderNull(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	below := 0
	const n = 20000
	for i := 0; i < n; i++ {
		if Fisher([]float64{rng.Float64(), rng.Float64(), rng.Float64()}) < 0.05 {
			below++
		}
	}
	rate := float64(below) / n
	if math.Abs(rate-0.05) > 0.01 {
		t.Fatalf("false-positive rate under the null should be ~5%%, got %.3f", rate)
	}
}

func TestTailIsCausal(t *testing.T) {
	tail := NewTail(10)
	for i := 0; i < 10; i++ {
		if p := tail.Step(float64(i)); p != 1 {
			t.Fatalf("warm-up must return p=1, got %v at %d", p, i)
		}
	}
	high := tail.Step(1000)
	low := tail.Step(-1000)
	if high >= low {
		t.Fatalf("a record-high score must get a smaller p-value: high=%v low=%v", high, low)
	}
	if high <= 0 || low > 1 {
		t.Fatalf("p-values out of range: %v %v", high, low)
	}
}

func TestFisherStreamsUsesGivenPValues(t *testing.T) {
	a := []float64{0.5, 0.01, 0.5, 0.5}
	b := []float64{0.5, 0.01, 0.5}
	out := FisherStreams([][]float64{a, b})
	if len(out) != 3 {
		t.Fatalf("output must be as long as the shortest stream, got %d", len(out))
	}
	if out[1] >= out[0] {
		t.Fatalf("agreeing small p-values must combine lower: %v vs %v", out[1], out[0])
	}
	if FisherStreams(nil) != nil {
		t.Fatal("no streams must give nil")
	}
	if FisherStreams([][]float64{{}}) != nil {
		t.Fatal("empty streams must give nil")
	}

	logs := NegLog10(out)
	if len(logs) != len(out) {
		t.Fatalf("NegLog10 must preserve length: %d vs %d", len(logs), len(out))
	}
	if logs[1] <= logs[0] {
		t.Fatalf("NegLog10 must flip the ordering: %v vs %v", logs[1], logs[0])
	}
	if got := NegLog10([]float64{0})[0]; got < 100 {
		t.Fatalf("a clamped zero p-value must give a large finite score, got %v", got)
	}
}

func TestCombineRewardsAgreement(t *testing.T) {
	n := 600
	a := make([]float64, n)
	b := make([]float64, n)
	rng := rand.New(rand.NewPCG(4, 5))
	for i := range a {
		a[i] = rng.Float64()
		b[i] = rng.Float64()
	}
	at := 500
	a[at], b[at] = 100, 100
	a[520] = 100

	scores := CombineScores([][]float64{a, b}, 50)
	if len(scores) != n {
		t.Fatalf("expected %d combined scores, got %d", n, len(scores))
	}
	if scores[at] <= scores[520] {
		t.Fatalf("agreement across detectors must beat a single detector: %v vs %v", scores[at], scores[520])
	}
	if Combine(nil, 10) != nil {
		t.Fatal("no streams must combine to nil")
	}
}
