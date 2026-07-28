package guard

import (
	"math"
	"math/rand/v2"
	"testing"
)

func countTrue(xs []bool) int {
	n := 0
	for _, x := range xs {
		if x {
			n++
		}
	}
	return n
}

func TestPersistenceDropsIsolatedSpikes(t *testing.T) {
	scores := make([]float64, 60)
	for i := range scores {
		scores[i] = 0.1
	}
	scores[10] = 9
	for i := 30; i < 36; i++ {
		scores[i] = 9
	}

	plain := Apply(scores, Options{Threshold: 1})
	if !plain[10] {
		t.Fatal("with no persistence the isolated spike must fire")
	}

	strict := Apply(scores, Options{Threshold: 1, Persist: 3, Of: 5})
	if strict[10] {
		t.Fatal("one point in five must not satisfy 3-of-5")
	}
	fired := -1
	for i, f := range strict {
		if f {
			fired = i
			break
		}
	}
	if fired != 32 {
		t.Fatalf("3-of-5 should fire on the third point of the run (index 32), got %d", fired)
	}
}

func TestRefractorySuppressesRepeats(t *testing.T) {
	scores := make([]float64, 50)
	for i := range scores {
		scores[i] = 5
	}
	none := Apply(scores, Options{Threshold: 1})
	if countTrue(none) != 50 {
		t.Fatalf("without a refractory period every point fires, got %d", countTrue(none))
	}
	spaced := Apply(scores, Options{Threshold: 1, Refractory: 9})
	if got := countTrue(spaced); got != 5 {
		t.Fatalf("a 9-point refractory over 50 points should leave 5 alarms, got %d", got)
	}
	first, second := -1, -1
	for i, f := range spaced {
		if f {
			if first < 0 {
				first = i
			} else if second < 0 {
				second = i
				break
			}
		}
	}
	if second-first != 10 {
		t.Fatalf("alarms must be 10 apart, got %d and %d", first, second)
	}
}

func TestWarmupAndSuppressionWindows(t *testing.T) {
	scores := make([]float64, 40)
	for i := range scores {
		scores[i] = 5
	}
	warm := Apply(scores, Options{Threshold: 1, Warmup: 20})
	for i := 0; i < 20; i++ {
		if warm[i] {
			t.Fatalf("nothing may fire during warm-up, got an alarm at %d", i)
		}
	}
	if !warm[20] {
		t.Fatal("the first post-warm-up point must fire")
	}

	quiet := Apply(scores, Options{Threshold: 1, Suppress: []Window{{Start: 10, End: 19}}})
	for i := 10; i <= 19; i++ {
		if quiet[i] {
			t.Fatalf("a suppression window must silence index %d", i)
		}
	}
	if !quiet[9] || !quiet[20] {
		t.Fatal("suppression must not leak outside its window")
	}

	if got := SuppressAround([]int{100, 5}, 10, 20); len(got) != 2 ||
		got[0] != (Window{90, 120}) || got[1] != (Window{0, 25}) {
		t.Fatalf("SuppressAround must clamp at zero: %+v", got)
	}
}

func TestSuppressionStillFeedsPersistence(t *testing.T) {
	scores := make([]float64, 30)
	for i := range scores {
		scores[i] = 5
	}
	out := Apply(scores, Options{Threshold: 1, Persist: 3, Of: 3, Suppress: []Window{{Start: 0, End: 14}}})
	for i := 0; i <= 14; i++ {
		if out[i] {
			t.Fatalf("suppressed index %d must not fire", i)
		}
	}
	if out[15] || out[16] {
		t.Fatal("the persistence window must refill after suppression, not carry stale hits")
	}
	if !out[17] {
		t.Fatal("three clean points after the window must fire")
	}
}

func TestFeedbackPenaltyRaisesTheBar(t *testing.T) {
	g := New(Options{Threshold: 10})
	if !g.Step(12) {
		t.Fatal("a score above the threshold must fire")
	}
	g.Penalize(8, 40)
	if g.Threshold() != 18 {
		t.Fatalf("one penalty step should raise the threshold to 18, got %v", g.Threshold())
	}
	if g.Step(12) {
		t.Fatal("the same score must not fire after a false-positive report")
	}
	for i := 0; i < 20; i++ {
		g.Penalize(8, 40)
	}
	if g.Threshold() != 50 {
		t.Fatalf("the penalty must cap at 40 over the base, got %v", g.Threshold())
	}
	g.ClearPenalty()
	if g.Threshold() != 10 || !g.Step(12) {
		t.Fatal("clearing the penalty must restore the base threshold")
	}
}

func TestCooldownRequiresEscalation(t *testing.T) {
	scores := []float64{5, 5, 5, 20, 5, 5}
	out := Apply(scores, Options{Threshold: 4, Cooldown: 2})
	if !out[0] {
		t.Fatal("the first alarm must fire")
	}
	if out[1] || out[2] {
		t.Fatal("after firing, a score below cooldown*threshold must stay quiet")
	}
	if !out[3] {
		t.Fatal("a score above cooldown*threshold must fire again")
	}
}

func TestGuardRaisesPrecisionOnNoisyStream(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 5))
	n := 6000
	scores := make([]float64, n)
	truth := make([]bool, n)
	for i := range scores {
		scores[i] = math.Abs(rng.NormFloat64())
		if rng.Float64() < 0.004 {
			scores[i] += 5
		}
	}
	for _, at := range []int{1000, 2500, 4000, 5200} {
		for k := 0; k < 12; k++ {
			scores[at+k] += 6
			truth[at+k] = true
		}
	}

	score := func(pred []bool) (hits, alarms int) {
		for i, p := range pred {
			if !p {
				continue
			}
			alarms++
			if truth[i] {
				hits++
			}
		}
		return hits, alarms
	}

	plainHits, plainAlarms := score(Apply(scores, Options{Threshold: 4}))
	guardHits, guardAlarms := score(Apply(scores, Options{Threshold: 4, Persist: 3, Of: 5, Refractory: 20}))

	plainPrec := float64(plainHits) / float64(plainAlarms)
	guardPrec := float64(guardHits) / float64(guardAlarms)
	if guardPrec <= plainPrec {
		t.Fatalf("the guard must raise alarm precision: %.3f vs %.3f", guardPrec, plainPrec)
	}
	if guardHits == 0 {
		t.Fatal("the guard must still catch the real events")
	}
	if guardAlarms >= plainAlarms {
		t.Fatalf("the guard must cut the alarm count: %d vs %d", guardAlarms, plainAlarms)
	}
}

func TestPresetsOrderByStrictness(t *testing.T) {
	rng := rand.New(rand.NewPCG(41, 43))
	n := 8000
	scores := make([]float64, n)
	for i := range scores {
		scores[i] = math.Abs(rng.NormFloat64())
	}
	for _, at := range []int{2000, 4000, 6000} {
		for k := 0; k < 15; k++ {
			scores[at+k] += 6
		}
	}

	names := []string{"sensitive", "balanced", "precise", "paranoid"}
	presets := Presets()
	if len(presets) != len(names) {
		t.Fatalf("expected %d presets, got %d", len(names), len(presets))
	}
	counts := make([]int, len(names))
	for i, name := range names {
		opt, ok := presets[name]
		if !ok {
			t.Fatalf("preset %q missing", name)
		}
		opt.Threshold = 3
		counts[i] = countTrue(Apply(scores, opt))
		if counts[i] == 0 {
			t.Fatalf("preset %q silenced everything", name)
		}
	}
	if counts[0] <= counts[1] {
		t.Fatalf("sensitive must raise more alarms than balanced: %d vs %d", counts[0], counts[1])
	}
	if counts[2] <= counts[3] {
		t.Fatalf("precise must raise more alarms than paranoid: %d vs %d", counts[2], counts[3])
	}
	if counts[3] >= counts[0] {
		t.Fatalf("paranoid must be the quietest: %d vs %d", counts[3], counts[0])
	}
}

func TestDefaultThresholdNeverFires(t *testing.T) {
	out := Apply([]float64{1e9, 1e9}, Options{})
	if countTrue(out) != 0 {
		t.Fatal("an unset threshold must be treated as +Inf, not zero")
	}
}
