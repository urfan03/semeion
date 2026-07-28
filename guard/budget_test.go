package guard

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestSolveThresholdHitsTheBudget(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 7))
	n := 10000
	scores := make([]float64, n)
	for i := range scores {
		scores[i] = math.Abs(rng.NormFloat64())
	}

	for _, b := range []Budget{{1, 1000}, {2, 1000}, {5, 1000}, {1, 100}} {
		thr := SolveThreshold(scores, b, Options{})
		got := countTrue(WithBudget(scores, b, Options{}))
		want := b.Alarms * n / b.Per
		if got > want {
			t.Fatalf("budget %d per %d: %d alarms exceeds the allowance of %d (threshold %v)",
				b.Alarms, b.Per, got, want, thr)
		}
		if got < want/2 {
			t.Fatalf("budget %d per %d: %d alarms wastes the allowance of %d", b.Alarms, b.Per, got, want)
		}
	}
}

func TestSolveThresholdRespectsThePolicy(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 13))
	n := 6000
	scores := make([]float64, n)
	for i := range scores {
		scores[i] = math.Abs(rng.NormFloat64())
	}
	for i := 1000; i < 1010; i++ {
		scores[i] += 8
	}

	b := Budget{Alarms: 2, Per: 1000}
	loose := SolveThreshold(scores, b, Options{})
	strict := SolveThreshold(scores, b, Options{Persist: 3, Of: 5})
	if strict > loose {
		t.Fatalf("a stricter policy needs a lower threshold to spend the same budget: %v vs %v", strict, loose)
	}
	if countTrue(WithBudget(scores, b, Options{Persist: 3, Of: 5})) > 2*n/1000 {
		t.Fatal("the solved threshold must respect the budget under the policy too")
	}
}

func TestSolveThresholdDegenerate(t *testing.T) {
	if !math.IsInf(SolveThreshold(nil, Budget{1, 100}, Options{}), 1) {
		t.Fatal("no scores means no threshold")
	}
	if !math.IsInf(SolveThreshold([]float64{1, 2}, Budget{}, Options{}), 1) {
		t.Fatal("an empty budget must never fire")
	}
	if !math.IsInf(SolveThreshold([]float64{math.NaN()}, Budget{1, 10}, Options{}), 1) {
		t.Fatal("all-NaN scores means no threshold")
	}
	flat := make([]float64, 100)
	if got := countTrue(WithBudget(flat, Budget{1, 100}, Options{})); got == 0 {
		t.Fatal("with identical scores the budget should still fire something")
	}
}

func TestRollingBaselineIsCausalAndRobust(t *testing.T) {
	n := 500
	values := make([]float64, n)
	for i := range values {
		values[i] = 10
	}
	values[300] = 1000

	base, scale := RollingBaseline(values, 50)
	if len(base) != n || len(scale) != n {
		t.Fatalf("shapes wrong: %d %d", len(base), len(scale))
	}
	if math.Abs(base[301]-10) > 1e-9 {
		t.Fatalf("a single outlier must not move a median baseline, got %v", base[301])
	}
	prefix, _ := RollingBaseline(values[:200], 50)
	for i := 0; i < 200; i++ {
		if math.Abs(base[i]-prefix[i]) > 1e-9 {
			t.Fatalf("the baseline must be causal, diverged at %d", i)
		}
	}
}

func TestGateByEffectDropsTinyDeviations(t *testing.T) {
	n := 400
	values := make([]float64, n)
	for i := range values {
		values[i] = 100
	}
	values[100] = 102
	values[200] = 160

	base, scale := RollingBaseline(values, 50)
	alarms := make([]bool, n)
	alarms[100], alarms[200] = true, true

	gated := GateByEffect(alarms, Effect{Values: values, Baseline: base, Scale: scale, MinAbs: 10})
	if gated[100] {
		t.Fatal("a 2-unit deviation must not survive a 10-unit floor")
	}
	if !gated[200] {
		t.Fatal("a 60-unit deviation must survive")
	}

	rel := GateByEffect(alarms, Effect{Values: values, Baseline: base, Scale: scale, MinRel: 3})
	if !rel[200] {
		t.Fatal("a large deviation must clear the relative gate")
	}

	none := GateByEffect(alarms, Effect{Values: values, Baseline: base, Scale: scale})
	if !none[100] || !none[200] {
		t.Fatal("with no thresholds set the gate must pass everything through")
	}
	if len(GateByEffect(nil, Effect{})) != 0 {
		t.Fatal("no alarms means no output")
	}
}

func TestGateByEffectRaisesPrecision(t *testing.T) {
	rng := rand.New(rand.NewPCG(21, 23))
	n := 6000
	values := make([]float64, n)
	truth := make([]bool, n)
	for i := range values {
		values[i] = 100 + rng.NormFloat64()
	}
	for _, at := range []int{1000, 2500, 4000} {
		for k := 0; k < 10; k++ {
			values[at+k] = 160
			truth[at+k] = true
		}
	}
	for i := 500; i < n; i += 700 {
		values[i] = 104
	}

	base, scale := RollingBaseline(values, 100)
	scores := make([]float64, n)
	for i := range values {
		s := 0.0
		if scale[i] > 0 {
			s = math.Abs(values[i]-base[i]) / scale[i]
		}
		scores[i] = s
	}
	raw := Apply(scores, Options{Threshold: 3, Warmup: 200})
	gated := GateByEffect(raw, Effect{Values: values, Baseline: base, Scale: scale, MinAbs: 20})

	score := func(pred []bool) (hits, alarms int) {
		for i, p := range pred {
			if p {
				alarms++
				if truth[i] {
					hits++
				}
			}
		}
		return hits, alarms
	}
	rh, ra := score(raw)
	gh, ga := score(gated)
	if ra == 0 || ga == 0 {
		t.Fatalf("both configurations must fire: %d %d", ra, ga)
	}
	if float64(gh)/float64(ga) <= float64(rh)/float64(ra) {
		t.Fatalf("effect gating must raise precision: %.3f vs %.3f", float64(gh)/float64(ga), float64(rh)/float64(ra))
	}
	if gh == 0 {
		t.Fatal("effect gating must keep the real events")
	}
}
