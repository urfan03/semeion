package correlate

import (
	"math"
	"testing"
)

// #7: given a target symptom series and several candidate series, the candidate
// that leads the target ranks first — the data-driven "what moved first".
func TestOrderByCausality(t *testing.T) {
	n := 300
	target := make([]float64, n)
	leader := make([]float64, n)   // moves 5 steps before the target
	follower := make([]float64, n) // moves 5 steps after the target
	unrelated := make([]float64, n)
	for i := 0; i < n; i++ {
		leader[i] = math.Sin(float64(i) * 0.25)
		unrelated[i] = math.Cos(float64(i) * 0.9)
	}
	for i := 0; i < n; i++ {
		if i-5 >= 0 {
			target[i] = leader[i-5] // target lags the leader by 5
		}
		if i-10 >= 0 {
			follower[i] = leader[i-10] // follower lags the target by 5 more
		}
	}
	ranks := OrderByCausality(target, map[string][]float64{
		"leader": leader, "follower": follower, "unrelated": unrelated,
	}, 12, 3)
	if len(ranks) != 3 {
		t.Fatalf("expected 3 ranks, got %d", len(ranks))
	}
	if ranks[0].Label != "leader" {
		t.Fatalf("the leading series should rank first, got %q (%+v)", ranks[0].Label, ranks)
	}
	if !ranks[0].Leads || ranks[0].Lag <= 0 {
		t.Fatalf("leader should be marked as leading with positive lag: %+v", ranks[0])
	}
}
