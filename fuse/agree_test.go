package fuse

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestBetaCDFMatchesKnownValues(t *testing.T) {
	cases := []struct{ x, a, b, want float64 }{
		{0.5, 1, 1, 0.5},
		{0.25, 1, 1, 0.25},
		{0.5, 2, 1, 0.25},
		{0.5, 1, 2, 0.75},
		{0.3, 3, 2, 0.0837},
		{0.9, 5, 5, 0.99910907},
	}
	for _, c := range cases {
		got := betaCDF(c.x, c.a, c.b)
		if math.Abs(got-c.want) > 1e-4 {
			t.Fatalf("betaCDF(%v,%v,%v) = %v, want %v", c.x, c.a, c.b, got, c.want)
		}
	}
	if betaCDF(0, 2, 3) != 0 || betaCDF(1, 2, 3) != 1 {
		t.Fatal("the endpoints must be 0 and 1")
	}
	for x := 0.05; x < 1; x += 0.05 {
		if lo, hi := betaCDF(x-0.04, 3, 4), betaCDF(x, 3, 4); hi < lo {
			t.Fatalf("betaCDF must be monotone: %v then %v", lo, hi)
		}
	}
}

func TestAgreeIsCalibratedUnderTheNull(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	const n, m = 20000, 5
	for _, k := range []int{1, 2, 3, 5} {
		below := 0
		buf := make([]float64, m)
		for i := 0; i < n; i++ {
			for j := range buf {
				buf[j] = rng.Float64()
			}
			if Agree(buf, k) < 0.05 {
				below++
			}
		}
		rate := float64(below) / n
		if math.Abs(rate-0.05) > 0.008 {
			t.Fatalf("k=%d: false-positive rate should be ~5%%, got %.4f", k, rate)
		}
	}
}

func TestAgreeDemandsMoreDetectorsAsKGrows(t *testing.T) {
	one := []float64{1e-6, 0.5, 0.5, 0.5}
	two := []float64{1e-6, 1e-6, 0.5, 0.5}
	all := []float64{1e-6, 1e-6, 1e-6, 1e-6}

	if Agree(one, 1) >= 0.01 {
		t.Fatalf("a single strong detector must pass k=1, got %v", Agree(one, 1))
	}
	if Agree(one, 2) <= Agree(two, 2) {
		t.Fatalf("k=2 must punish a lone detector: %v vs %v", Agree(one, 2), Agree(two, 2))
	}
	if Agree(one, 2) <= 0.05 {
		t.Fatalf("one of four firing must not satisfy k=2, got %v", Agree(one, 2))
	}
	if Agree(all, 4) >= Agree(two, 4) {
		t.Fatalf("unanimity must beat partial agreement at k=4: %v vs %v", Agree(all, 4), Agree(two, 4))
	}
	if Agree(nil, 2) != 1 || Agree([]float64{0.001}, 3) != 1 {
		t.Fatal("asking for more detectors than exist must give no evidence")
	}
}

func TestMajorityPicksTheRightK(t *testing.T) {
	three := []float64{1e-6, 1e-6, 0.5}
	if Majority(three) != Agree(three, 2) {
		t.Fatal("majority of 3 must be k=2")
	}
	four := []float64{1e-6, 1e-6, 1e-6, 0.5}
	if Majority(four) != Agree(four, 3) {
		t.Fatal("majority of 4 must be k=3")
	}
	if Majority(nil) != 1 {
		t.Fatal("no detectors means no evidence")
	}
}

func TestAgreeBeatsFisherOnALyingDetector(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 17))
	n := 4000
	streams := make([][]float64, 4)
	for i := range streams {
		streams[i] = make([]float64, n)
	}
	truth := make([]bool, n)
	for i := 0; i < n; i++ {
		event := i%200 == 0
		truth[i] = event
		for d := 0; d < 3; d++ {
			p := 0.5
			if event {
				p = 1e-5
			}
			streams[d][i] = p
		}
		streams[3][i] = 0.5
		if rng.Float64() < 0.2 {
			streams[3][i] = 1e-9
		}
	}

	fisher := FisherStreams(streams)
	agree := AgreeStreams(streams, 3)
	count := func(p []float64) (hits, false_ int) {
		for i, v := range p {
			if v < 1e-3 {
				if truth[i] {
					hits++
				} else {
					false_++
				}
			}
		}
		return hits, false_
	}
	fh, ff := count(fisher)
	ah, af := count(agree)
	if ah < fh {
		t.Fatalf("agreement must not lose real events: %d vs %d", ah, fh)
	}
	if af >= ff {
		t.Fatalf("agreement must cut the lying detector's false alarms: %d vs %d", af, ff)
	}
	if AgreeStreams(nil, 2) != nil {
		t.Fatal("no streams must give nil")
	}
}

func TestVoteCountsFirings(t *testing.T) {
	p := []float64{0.001, 0.002, 0.5, math.NaN()}
	if !Vote(p, 0.01, 2) {
		t.Fatal("two p-values below tau must satisfy k=2")
	}
	if Vote(p, 0.01, 3) {
		t.Fatal("only two fired, so k=3 must fail")
	}
}
