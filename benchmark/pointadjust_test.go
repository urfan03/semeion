package benchmark

import (
	"math"
	"testing"
)

func TestSegmentsFindsContiguousRuns(t *testing.T) {
	segs := Segments([]bool{false, true, true, false, false, true, false, true})
	want := [][2]int{{1, 2}, {5, 5}, {7, 7}}
	if len(segs) != len(want) {
		t.Fatalf("expected %v, got %v", want, segs)
	}
	for i := range want {
		if segs[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, segs)
		}
	}
	if Segments(nil) != nil {
		t.Fatal("no labels must give no segments")
	}
}

func TestPointAdjustExpandsHitSegments(t *testing.T) {
	labels := []bool{false, true, true, true, false, true, true, false}
	pred := []bool{false, false, true, false, false, false, false, true}
	adj := PointAdjust(pred, labels)
	want := []bool{false, true, true, true, false, false, false, true}
	for i := range want {
		if adj[i] != want[i] {
			t.Fatalf("point adjustment wrong: got %v want %v", adj, want)
		}
	}

	res := PointAdjustedScore(pred, labels)
	if res.TP != 3 || res.FN != 2 || res.FP != 1 {
		t.Fatalf("adjusted confusion wrong: %+v", res)
	}
	raw := Confusion(pred, labels)
	if raw.TP != 1 {
		t.Fatalf("raw confusion should not be adjusted: %+v", raw)
	}
	if res.F1 <= raw.F1 {
		t.Fatalf("point adjustment must not lower F1: %v vs %v", res.F1, raw.F1)
	}
}

func TestBestPointAdjustedF1PicksThreshold(t *testing.T) {
	scores := []float64{0.1, 0.2, 0.9, 0.15, 0.1, 0.05, 0.8, 0.1}
	labels := []bool{false, false, true, true, false, false, true, false}
	best, thr := BestPointAdjustedF1(scores, labels)
	if best.F1 != 1 {
		t.Fatalf("a threshold reaching every segment should score F1=1, got %+v at %v", best, thr)
	}
	if thr <= 0.2 || thr > 0.8 {
		t.Fatalf("threshold should sit between noise and signal, got %v", thr)
	}

	plain, _ := BestF1(scores, labels)
	if plain.F1 >= best.F1 {
		t.Fatalf("unadjusted F1 should be the stricter metric: %v vs %v", plain.F1, best.F1)
	}

	none, noneThr := BestPointAdjustedF1([]float64{1, 2, 3}, []bool{false, false, false})
	if none.F1 != 0 || !math.IsInf(noneThr, 1) {
		t.Fatalf("with no anomalies there is no usable threshold: %+v %v", none, noneThr)
	}
}

func TestAUCPRRanksDetectors(t *testing.T) {
	labels := []bool{false, false, false, true, true, false, false, false}
	perfect := []float64{0, 0.1, 0.2, 1, 0.9, 0.3, 0.1, 0}
	random := []float64{1, 0.9, 0.8, 0.2, 0.1, 0.7, 0.6, 0.5}

	if got := AUCPR(perfect, labels); math.Abs(got-1) > 1e-9 {
		t.Fatalf("perfect ranking should give AUC-PR 1, got %v", got)
	}
	if got := AUCPR(random, labels); got >= 0.5 {
		t.Fatalf("inverted ranking should score poorly, got %v", got)
	}
	if AUCPR(perfect, []bool{false, false, false, false, false, false, false, false}) != 0 {
		t.Fatal("AUC-PR is undefined without positives and must be 0")
	}

	base := float64(2) / float64(len(labels))
	flat := make([]float64, len(labels))
	if got := AUCPR(flat, labels); math.Abs(got-base) > 1e-9 {
		t.Fatalf("all-equal scores should give the positive rate %v, got %v", base, got)
	}
}

func TestPointAdjustedAUCPRIsBounded(t *testing.T) {
	labels := []bool{false, false, true, true, true, false, false, false}
	scores := []float64{0.1, 0.1, 0.2, 0.1, 0.9, 0.1, 0.1, 0.1}
	pa := PointAdjustedAUCPR(scores, labels)
	plain := AUCPR(scores, labels)
	if pa < plain {
		t.Fatalf("point-adjusted AUC-PR should not be below the raw one: %v vs %v", pa, plain)
	}
	if pa < 0 || pa > 1 {
		t.Fatalf("AUC-PR out of range: %v", pa)
	}
}
