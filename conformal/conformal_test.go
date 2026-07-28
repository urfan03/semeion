package conformal

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestPValueRespectsTheGuarantee(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	const nCal, nTest = 2000, 40000
	for _, alpha := range []float64{0.1, 0.05, 0.01} {
		cal := make([]float64, nCal)
		for i := range cal {
			cal[i] = rng.NormFloat64()
		}
		c := New(cal, alpha)
		fired := 0
		for i := 0; i < nTest; i++ {
			if c.Alarm(rng.NormFloat64()) {
				fired++
			}
		}
		rate := float64(fired) / nTest
		if rate > alpha*1.25 {
			t.Fatalf("alpha=%v: conformal must hold the false-alarm rate, got %.4f", alpha, rate)
		}
		if rate < alpha*0.5 {
			t.Fatalf("alpha=%v: the guarantee should be tight, not vacuous, got %.4f", alpha, rate)
		}
	}
}

func TestPValueIsMonotoneAndBounded(t *testing.T) {
	c := New([]float64{1, 2, 3, 4, 5}, 0.2)
	if c.Size() != 5 {
		t.Fatalf("expected 5 calibration points, got %d", c.Size())
	}
	if p := c.P(10); p != 1.0/6.0 {
		t.Fatalf("a record high must give the smallest attainable p-value, got %v", p)
	}
	if p := c.P(0); p != 1 {
		t.Fatalf("a record low must give p=1, got %v", p)
	}
	if c.P(4) <= c.P(5) {
		t.Fatalf("p must fall as the score rises: %v vs %v", c.P(4), c.P(5))
	}
	empty := New(nil, 0.05)
	if empty.P(99) != 1 || !math.IsInf(empty.Threshold(), 1) {
		t.Fatal("with no calibration data there is no evidence and no threshold")
	}
}

func TestThresholdMatchesTheAlarmRule(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 7))
	cal := make([]float64, 1000)
	for i := range cal {
		cal[i] = rng.NormFloat64()
	}
	for _, alpha := range []float64{0.001, 0.01, 0.02, 0.1, 0.5, 0.9} {
		c := New(cal, alpha)
		thr := c.Threshold()
		for i := 0; i < 3000; i++ {
			x := rng.NormFloat64() * 1.5
			if c.Alarm(x) != (x > thr) {
				t.Fatalf("alpha=%v: Threshold is the highest quiet score, so alarms sit strictly above it: score %v alarm=%v thr=%v",
					alpha, x, c.Alarm(x), thr)
			}
		}
		if math.IsInf(thr, 0) {
			continue
		}
		if c.Alarm(thr) {
			t.Fatalf("alpha=%v: the threshold itself must not alarm", alpha)
		}
		if !c.Alarm(math.Nextafter(thr, math.Inf(1))) {
			t.Fatalf("alpha=%v: the next value above the threshold must alarm", alpha)
		}
	}
}

func TestMinCalibrationAndGuarantee(t *testing.T) {
	if got := MinCalibration(0.01); got != 99 {
		t.Fatalf("alpha=0.01 needs 99 calibration points to be attainable, got %d", got)
	}
	if New(make([]float64, 50), 0.01).Threshold() != math.Inf(1) {
		t.Fatal("too few calibration points must make the threshold unreachable")
	}
	if g := Guarantee(0.05, 999); math.Abs(g-0.05) > 1e-9 {
		t.Fatalf("with n+1 a multiple of 1/alpha the bound should be exact, got %v", g)
	}
	if Guarantee(0.05, 0) != 1 {
		t.Fatal("no calibration data gives no guarantee")
	}
}

func TestMondrianConditionsOnTheSeasonalSlot(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 13))
	period := 24
	n := 4800
	scores := make([]float64, n)
	for i := range scores {
		peak := 0.0
		if i%period == 12 {
			peak = 10
		}
		scores[i] = peak + rng.NormFloat64()
	}

	plain := Probabilities(scores, StreamOptions{Alpha: 0.01, Calibration: 2400})
	seasonal := Probabilities(scores, StreamOptions{Alpha: 0.01, Calibration: 2400, Period: period})

	countPeakAlarms := func(p []float64) int {
		n := 0
		for i := 2400; i < len(p); i++ {
			if i%period == 12 && p[i] <= 0.01 {
				n++
			}
		}
		return n
	}
	if countPeakAlarms(plain) == 0 {
		t.Fatal("expected the unconditional model to alarm on every seasonal peak")
	}
	if countPeakAlarms(seasonal) >= countPeakAlarms(plain) {
		t.Fatalf("slot-conditioned calibration must stop firing on the daily peak: %d vs %d",
			countPeakAlarms(seasonal), countPeakAlarms(plain))
	}

	m := NewMondrian(scores[:2400], 0, period, 0.01)
	if m.Period() != period || m.Smallest() < 90 {
		t.Fatalf("each slot should hold ~100 points, smallest=%d", m.Smallest())
	}
}

func TestMondrianStillCatchesRealAnomalies(t *testing.T) {
	rng := rand.New(rand.NewPCG(17, 19))
	period := 24
	n := 4800
	scores := make([]float64, n)
	for i := range scores {
		peak := 0.0
		if i%period == 12 {
			peak = 10
		}
		scores[i] = peak + rng.NormFloat64()
	}
	at := 3600 - 3600%period + 12
	scores[at] += 40

	p := Probabilities(scores, StreamOptions{Alpha: 0.01, Calibration: 2400, Period: period})
	if p[at] > 0.01 {
		t.Fatalf("a genuine spike on top of the seasonal peak must still alarm, got p=%v", p[at])
	}
}

func TestProbabilitiesShortInput(t *testing.T) {
	p := Probabilities([]float64{1, 2, 3}, StreamOptions{Alpha: 0.05, Calibration: 100})
	if len(p) != 3 {
		t.Fatalf("expected 3 values, got %d", len(p))
	}
	for _, v := range p {
		if v != 1 {
			t.Fatalf("too little data must yield no evidence, got %v", v)
		}
	}
	s := Scores([]float64{1, 2, 3}, StreamOptions{Alpha: 0.05, Calibration: 100})
	for _, v := range s {
		if v != 0 {
			t.Fatalf("p=1 must map to score 0, got %v", v)
		}
	}
}

func TestSlidingCalibrationTracksDrift(t *testing.T) {
	rng := rand.New(rand.NewPCG(23, 29))
	n := 6000
	scores := make([]float64, n)
	for i := range scores {
		scores[i] = 0.002*float64(i) + rng.NormFloat64()
	}
	fixed := Probabilities(scores, StreamOptions{Alpha: 0.01, Calibration: 1500})
	slid := Probabilities(scores, StreamOptions{Alpha: 0.01, Calibration: 1500, Slide: true})

	count := func(p []float64) int {
		n := 0
		for i := 4500; i < len(p); i++ {
			if p[i] <= 0.01 {
				n++
			}
		}
		return n
	}
	if count(slid) >= count(fixed) {
		t.Fatalf("a sliding window must stop treating drift as anomalous: %d vs %d", count(slid), count(fixed))
	}
}
