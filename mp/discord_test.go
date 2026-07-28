package mp

import (
	"math"
	"testing"
)

func TestAutoWindowStaysBounded(t *testing.T) {
	if got := AutoWindow(4000); got != defaultWindow {
		t.Fatalf("expected the tuned default window, got %d", got)
	}
	if got := AutoWindow(10); got != 5 {
		t.Fatalf("a short series must shrink the window, got %d", got)
	}
	if got := AutoWindow(3); got != 0 {
		t.Fatalf("a series too short for any window must return 0, got %d", got)
	}
}

func TestScoresLengthAndFallback(t *testing.T) {
	if got := Scores(nil, Options{}); len(got) != 0 {
		t.Fatalf("empty input must give empty output, got %d", len(got))
	}
	short := Scores([]float64{1, 2, 3}, Options{Window: 8})
	if len(short) != 3 {
		t.Fatalf("output must match input length, got %d", len(short))
	}
	for _, v := range short {
		if v != 0 {
			t.Fatalf("a series too short for the window must score 0, got %v", v)
		}
	}

	n := 400
	ts := make([]float64, n)
	for i := range ts {
		ts[i] = math.Sin(float64(i) * 0.3)
	}
	spread := Scores(ts, Options{Window: 20, Spread: true})
	raw := Scores(ts, Options{Window: 20})
	if len(spread) != n || len(raw) != n {
		t.Fatalf("both modes must return n scores: %d %d", len(spread), len(raw))
	}
	tail := 0
	for i := n - 19; i < n; i++ {
		if raw[i] != 0 {
			tail++
		}
	}
	if tail != 0 {
		t.Fatal("unspread mode leaves the trailing partial window at zero")
	}
}

func TestConstantFlatSuppressesRepeatedFlats(t *testing.T) {
	const n, m = 600, 30
	ts := make([]float64, n)
	for i := range ts {
		ts[i] = 5
	}
	for i := 200; i < 200+m; i++ {
		ts[i] = 5 + math.Sin(float64(i)*1.3)*3
	}

	plain := Scores(ts, Options{Window: m, Spread: true, FlatAsDiscord: true})
	flat := Scores(ts, Options{Window: m, Spread: true})

	quiet := func(s []float64) (max float64) {
		for i := 0; i < n; i++ {
			if i >= 200-2*m && i < 200+2*m {
				continue
			}
			if s[i] > max {
				max = s[i]
			}
		}
		return max
	}
	if quiet(plain) == 0 {
		t.Fatal("expected the plain profile to misfire on at least one constant window")
	}
	if quiet(flat) != 0 {
		t.Fatalf("identical constant windows must be distance 0, got %.3f (plain %.3f)", quiet(flat), quiet(plain))
	}
	var inBurst float64
	for i := 200; i < 200+m; i++ {
		if flat[i] > inBurst {
			inBurst = flat[i]
		}
	}
	if inBurst <= quiet(flat) {
		t.Fatalf("the real discord must still stand out: %.3f vs %.3f", inBurst, quiet(flat))
	}
}

func TestConstantFlatIsConsistentAcrossWindows(t *testing.T) {
	const n, m = 500, 25
	ts := make([]float64, n)
	for i := range ts {
		ts[i] = 3.5
	}
	plain := stomp(ts, m, false)
	flat := stomp(ts, m, true)

	want := math.Sqrt(2 * float64(m))
	for i, v := range plain {
		if math.Abs(v-want) > 1e-9 {
			t.Fatalf("the plain profile calls every constant window a maximal discord: index %d scored %v, want %v", i, v, want)
		}
	}
	for i, v := range flat {
		if v != 0 {
			t.Fatalf("every window of a constant series is its own neighbour: index %d scored %v", i, v)
		}
	}
}
