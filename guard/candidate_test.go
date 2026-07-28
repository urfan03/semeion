package guard

import (
	"math"
	"testing"
)

func TestCandidatesGroupContiguousRuns(t *testing.T) {
	scores := []float64{0, 5, 6, 0, 0, 9, 0, 0, 0, 4, 4}
	got := Candidates(scores, 3, 0)
	if len(got) != 3 {
		t.Fatalf("expected 3 candidates, got %d: %+v", len(got), got)
	}
	if got[0].Start != 1 || got[0].End != 2 || got[0].Length != 2 {
		t.Fatalf("first candidate wrong: %+v", got[0])
	}
	if got[0].Peak != 2 || got[0].Score != 6 {
		t.Fatalf("peak must be the highest point in the run: %+v", got[0])
	}
	if got[1].Start != 5 || got[1].End != 5 {
		t.Fatalf("second candidate wrong: %+v", got[1])
	}
	if got[2].Start != 9 || got[2].End != 10 {
		t.Fatalf("third candidate wrong: %+v", got[2])
	}
}

func TestCandidatesBridgeShortGaps(t *testing.T) {
	scores := []float64{0, 5, 0, 5, 0, 0, 0, 5}
	tight := Candidates(scores, 3, 0)
	if len(tight) != 3 {
		t.Fatalf("with no gap tolerance every hit is its own candidate, got %d", len(tight))
	}
	bridged := Candidates(scores, 3, 1)
	if len(bridged) != 2 {
		t.Fatalf("a one-point gap must merge the first two, got %d: %+v", len(bridged), bridged)
	}
	if bridged[0].Start != 1 || bridged[0].End != 3 || bridged[0].Length != 3 {
		t.Fatalf("merged candidate wrong: %+v", bridged[0])
	}
	if bridged[1].Start != 7 {
		t.Fatalf("a three-point gap must not merge: %+v", bridged[1])
	}
}

func TestCandidatesIgnoreNaN(t *testing.T) {
	scores := []float64{math.NaN(), 5, math.NaN(), 5}
	got := Candidates(scores, 3, 0)
	if len(got) != 2 {
		t.Fatalf("NaN must break runs, not join them: %+v", got)
	}
	if len(Candidates(nil, 1, 0)) != 0 {
		t.Fatal("no scores means no candidates")
	}
	if len(Candidates([]float64{0, 1}, 5, 0)) != 0 {
		t.Fatal("nothing over the threshold means no candidates")
	}
}

func TestCandidatesFromPUsesAlpha(t *testing.T) {
	p := []float64{0.5, 1e-4, 1e-6, 0.5, 0.5, 1e-5}
	got := CandidatesFromP(p, 1e-3, 0)
	if len(got) != 2 {
		t.Fatalf("expected 2 candidates, got %d: %+v", len(got), got)
	}
	if math.Abs(got[0].MinP-1e-6) > 1e-15 {
		t.Fatalf("MinP must be the strongest p-value in the run, got %v", got[0].MinP)
	}
	if got[0].Length != 2 || got[0].Peak != 2 {
		t.Fatalf("run bookkeeping wrong: %+v", got[0])
	}
	if math.Abs(got[1].MinP-1e-5) > 1e-14 {
		t.Fatalf("second candidate MinP wrong: %v", got[1].MinP)
	}
}

func TestSidakCorrectsForRunLength(t *testing.T) {
	if got := SidakP(0.01, 1); math.Abs(got-0.01) > 1e-12 {
		t.Fatalf("a single point needs no correction, got %v", got)
	}
	long := SidakP(0.01, 100)
	if long <= 0.01 {
		t.Fatalf("scanning 100 points must inflate the p-value, got %v", long)
	}
	if long > 1 {
		t.Fatalf("the corrected p-value must stay a probability, got %v", long)
	}
	if math.Abs(long-(1-math.Pow(0.99, 100))) > 1e-12 {
		t.Fatalf("Sidak formula wrong: %v", long)
	}
	if SidakP(0, 10) != 0 || SidakP(1, 10) != 1 {
		t.Fatal("the endpoints must pass through")
	}
	if math.Abs(SidakP(0.01, 0)-0.01) > 1e-15 {
		t.Fatalf("a non-positive length must be treated as 1, got %v", SidakP(0.01, 0))
	}
}

func TestMaskExpandsOrPeaksOnly(t *testing.T) {
	cands := []Candidate{{Start: 1, End: 3, Peak: 2}, {Start: 6, End: 6, Peak: 6}}
	full := Mask(8, cands, nil, false)
	for i := 1; i <= 3; i++ {
		if !full[i] {
			t.Fatalf("the whole run must be masked, missing %d", i)
		}
	}
	if !full[6] || full[0] || full[4] {
		t.Fatalf("mask leaked: %v", full)
	}

	peaks := Mask(8, cands, nil, true)
	if !peaks[2] || peaks[1] || peaks[3] {
		t.Fatalf("peak-only mask wrong: %v", peaks)
	}

	filtered := Mask(8, cands, []bool{false, true}, false)
	if filtered[2] {
		t.Fatal("a rejected candidate must not be masked")
	}
	if !filtered[6] {
		t.Fatal("an accepted candidate must be masked")
	}
}
