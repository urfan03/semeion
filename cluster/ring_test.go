package cluster

import (
	"fmt"
	"testing"
)

func TestRingDeterministicAndComplete(t *testing.T) {
	r := New([]string{"a:8080", "b:8080", "c:8080"}, 128)
	key := "job-checkout-latency"
	o1 := r.Owner(key)
	o2 := r.Owner(key)
	if o1 == "" || o1 != o2 {
		t.Fatalf("owner must be deterministic and non-empty: %q vs %q", o1, o2)
	}
	members := map[string]bool{}
	for _, m := range r.Members() {
		members[m] = true
	}
	if !members[o1] {
		t.Fatalf("owner %q must be a ring member of %v", o1, r.Members())
	}
}

func TestRingBalanced(t *testing.T) {
	members := []string{"a", "b", "c", "d"}
	r := New(members, 256)
	counts := map[string]int{}
	const n = 20000
	for i := 0; i < n; i++ {
		counts[r.Owner(fmt.Sprintf("key-%d", i))]++
	}
	if len(counts) != len(members) {
		t.Fatalf("every member should own some keys, got %v", counts)
	}
	for m, c := range counts {
		frac := float64(c) / float64(n)
		if frac < 0.15 || frac > 0.35 {
			t.Fatalf("member %s owns %.1f%% of keys — unbalanced", m, frac*100)
		}
	}
}

func TestRingSingleAndEmpty(t *testing.T) {
	if New(nil, 128).Owner("x") != "" {
		t.Fatal("empty ring must return empty owner")
	}
	solo := New([]string{"only:8080"}, 128)
	if solo.Owner("anything") != "only:8080" {
		t.Fatal("single-member ring must own every key")
	}
}
