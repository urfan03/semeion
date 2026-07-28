package benchmark

import (
	"math"
	"testing"
)

func TestRangeRecallRewardsCoverage(t *testing.T) {
	labels := []bool{false, true, true, true, true, false, false, false}
	full := []bool{false, true, true, true, true, false, false, false}
	one := []bool{false, false, false, true, false, false, false, false}

	opt := RangeOptions{Alpha: 0.5}
	if got := RangeRecall(full, labels, opt); math.Abs(got-1) > 1e-9 {
		t.Fatalf("complete coverage must score 1, got %v", got)
	}
	partial := RangeRecall(one, labels, opt)
	if partial <= 0.5 || partial >= 1 {
		t.Fatalf("one hit earns the existence half plus a slice of overlap, got %v", partial)
	}
	if got := RangeRecall([]bool{false, false, false, false, false, false, false, false}, labels, opt); got != 0 {
		t.Fatalf("no detection must score 0, got %v", got)
	}
}

func TestRangeRecallPenalisesFragmentation(t *testing.T) {
	labels := []bool{true, true, true, true, true, true, false}
	whole := []bool{true, true, true, true, true, true, false}
	split := []bool{true, true, false, false, true, true, false}

	opt := RangeOptions{Alpha: 0}
	if RangeRecall(split, labels, opt) >= RangeRecall(whole, labels, opt) {
		t.Fatal("two fragments covering the same range must score below one contiguous match")
	}
}

func TestRangeBiasShiftsCredit(t *testing.T) {
	labels := []bool{false, true, true, true, true, false}
	early := []bool{false, true, true, false, false, false}
	late := []bool{false, false, false, true, true, false}

	front := RangeOptions{Alpha: 0, Bias: BiasFront}
	if RangeRecall(early, labels, front) <= RangeRecall(late, labels, front) {
		t.Fatal("front bias must prefer detecting the start of a range")
	}
	back := RangeOptions{Alpha: 0, Bias: BiasBack}
	if RangeRecall(late, labels, back) <= RangeRecall(early, labels, back) {
		t.Fatal("back bias must prefer detecting the end of a range")
	}
	flat := RangeOptions{Alpha: 0, Bias: BiasFlat}
	if math.Abs(RangeRecall(early, labels, flat)-RangeRecall(late, labels, flat)) > 1e-9 {
		t.Fatal("flat bias must treat both halves alike")
	}
	mid := RangeOptions{Alpha: 0, Bias: BiasMiddle}
	if RangeRecall([]bool{false, false, true, true, false, false}, labels, mid) <= RangeRecall(early, labels, mid) {
		t.Fatal("middle bias must prefer the centre of a range")
	}
}

func TestRangePrecisionPenalisesSpuriousRanges(t *testing.T) {
	labels := []bool{false, true, true, false, false, false, false, false}
	tight := []bool{false, true, true, false, false, false, false, false}
	noisy := []bool{false, true, true, false, false, true, true, false}

	opt := RangeOptions{}
	if got := RangePrecision(tight, labels, opt); math.Abs(got-1) > 1e-9 {
		t.Fatalf("an exact prediction must have precision 1, got %v", got)
	}
	if RangePrecision(noisy, labels, opt) >= 1 {
		t.Fatal("a spurious predicted range must cost precision")
	}

	p, r, f1 := RangeF1(tight, labels, RangeOptions{Alpha: 0.5})
	if p != 1 || r != 1 || math.Abs(f1-1) > 1e-9 {
		t.Fatalf("an exact match must be perfect on all three: %v %v %v", p, r, f1)
	}
	_, _, best, thr := BestRangeF1([]float64{0, 1, 1, 0, 0, 0, 0, 0}, labels, RangeOptions{Alpha: 0.5})
	if math.Abs(best-1) > 1e-9 || math.IsInf(thr, 1) {
		t.Fatalf("the sweep should find the perfect threshold: f1=%v thr=%v", best, thr)
	}
}

func TestPointAdjustKRequiresCoverage(t *testing.T) {
	labels := []bool{false, true, true, true, true, false}
	one := []bool{false, false, true, false, false, false}

	full := PointAdjustK(one, labels, 0)
	if !full[1] || !full[4] {
		t.Fatal("k=0 must behave like plain point adjustment")
	}
	strict := PointAdjustK(one, labels, 0.5)
	if strict[1] || strict[4] {
		t.Fatal("one hit in four must not satisfy k=50%")
	}
	loose := PointAdjustK(one, labels, 0.25)
	if !loose[1] || !loose[4] {
		t.Fatal("one hit in four does satisfy k=25%")
	}

	scores := []float64{0, 0, 0.9, 0, 0, 0}
	pa, _ := BestPointAdjustedF1(scores, labels)
	pak, _ := BestPointAdjustedKF1(scores, labels, 0.5)
	if pak.F1 >= pa.F1 {
		t.Fatalf("PA%%K must be the stricter metric: %v vs %v", pak.F1, pa.F1)
	}
}

func TestVUSPrefersRealDetectors(t *testing.T) {
	n := 400
	labels := make([]bool, n)
	for i := 200; i < 210; i++ {
		labels[i] = true
	}
	good := make([]float64, n)
	near := make([]float64, n)
	bad := make([]float64, n)
	for i := range good {
		good[i] = 0.1
		near[i] = 0.1
		bad[i] = 0.1
	}
	for i := 200; i < 210; i++ {
		good[i] = 1
	}
	for i := 214; i < 224; i++ {
		near[i] = 1
	}
	for i := 50; i < 60; i++ {
		bad[i] = 1
	}

	gRoc, gPR := VUS(good, labels, 10)
	nRoc, nPR := VUS(near, labels, 10)
	bRoc, bPR := VUS(bad, labels, 10)

	if gRoc <= nRoc || nRoc <= bRoc {
		t.Fatalf("VUS-ROC must order exact > near-miss > wrong: %.4f %.4f %.4f", gRoc, nRoc, bRoc)
	}
	if gPR <= bPR {
		t.Fatalf("VUS-PR must prefer the correct detector: %.4f vs %.4f", gPR, bPR)
	}
	for _, v := range []float64{gRoc, gPR, nRoc, nPR, bRoc, bPR} {
		if v < 0 || v > 1 {
			t.Fatalf("VUS out of range: %v", v)
		}
	}

	zeroRoc, _ := RangeAUC(good, labels, 0)
	if zeroRoc <= 0.9 {
		t.Fatalf("with no buffer an exact detector should still near-max ROC, got %v", zeroRoc)
	}
	if roc, pr := RangeAUC(good, make([]bool, n), 5); roc != 0 || pr != 0 {
		t.Fatalf("no positives means no curve: %v %v", roc, pr)
	}
}

func TestVUSToleratesNearMissesMoreThanPointMetrics(t *testing.T) {
	n := 300
	labels := make([]bool, n)
	for i := 100; i < 110; i++ {
		labels[i] = true
	}
	near := make([]float64, n)
	for i := range near {
		near[i] = 0.1
	}
	for i := 112; i < 122; i++ {
		near[i] = 1
	}

	pa, _ := BestPointAdjustedF1(near, labels)
	_, vusPR := VUS(near, labels, 12)
	plain := AUCPR(near, labels)
	if pa.F1 > 0.1 {
		t.Fatalf("point adjustment gives a near miss almost no credit, only the degenerate flag-everything threshold: %v", pa.F1)
	}
	if vusPR <= plain {
		t.Fatalf("VUS-PR must give a near miss partial credit: %v vs %v", vusPR, plain)
	}
}
