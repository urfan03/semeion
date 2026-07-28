package fdr

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

func bhByDefinition(sorted []float64, q float64) int {
	m := float64(len(sorted))
	cut := 0
	for k, p := range sorted {
		if p <= float64(k+1)/m*q {
			cut = k + 1
		}
	}
	return cut
}

func TestBHFollowsTheStepUpRule(t *testing.T) {
	rng := rand.New(rand.NewPCG(31, 37))
	for trial := 0; trial < 200; trial++ {
		m := 5 + rng.IntN(60)
		p := make([]float64, m)
		for i := range p {
			p[i] = rng.Float64()
			if rng.Float64() < 0.3 {
				p[i] *= 0.01
			}
		}
		sorted := append([]float64(nil), p...)
		sortFloats(sorted)
		q := 0.05 + 0.1*rng.Float64()

		want := bhByDefinition(sorted, q)
		thr, rej := BH(p, q)
		if got := countTrue(rej); got != want {
			t.Fatalf("m=%d q=%.3f: rejected %d, the step-up rule says %d", m, q, got, want)
		}
		if want == 0 {
			continue
		}
		if thr != sorted[want-1] {
			t.Fatalf("the threshold must be the largest rejected p-value: %v vs %v", thr, sorted[want-1])
		}
		for i, r := range rej {
			if r != (p[i] <= thr) {
				t.Fatalf("rejection set must be exactly {p <= threshold}, index %d", i)
			}
		}
	}
}

func sortFloats(xs []float64) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

func TestBHRejectsNothingUnderTheNull(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	trials, falseRuns := 200, 0
	for i := 0; i < trials; i++ {
		p := make([]float64, 500)
		for j := range p {
			p[j] = rng.Float64()
		}
		if _, rej := BH(p, 0.05); countTrue(rej) > 0 {
			falseRuns++
		}
	}
	if rate := float64(falseRuns) / float64(trials); rate > 0.1 {
		t.Fatalf("under a pure null BH should almost never reject, got %.3f of runs", rate)
	}
}

func TestBHControlsFDR(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 5))
	const trials, m, signals = 300, 1000, 100
	var fdrSum, powerSum float64
	for i := 0; i < trials; i++ {
		p := make([]float64, m)
		truth := make([]bool, m)
		for j := range p {
			p[j] = rng.Float64()
		}
		for j := 0; j < signals; j++ {
			p[j] = rng.Float64() * 1e-4
			truth[j] = true
		}
		_, rej := BH(p, 0.1)
		tp, fp := 0, 0
		for j, r := range rej {
			if !r {
				continue
			}
			if truth[j] {
				tp++
			} else {
				fp++
			}
		}
		if tp+fp > 0 {
			fdrSum += float64(fp) / float64(tp+fp)
		}
		powerSum += float64(tp) / float64(signals)
	}
	gotFDR := fdrSum / trials
	gotPower := powerSum / trials
	if gotFDR > 0.1 {
		t.Fatalf("BH must hold FDR at 0.1, got %.4f", gotFDR)
	}
	if gotPower < 0.9 {
		t.Fatalf("with very strong signals BH should recover nearly all, got power %.4f", gotPower)
	}
}

func TestBYIsMoreConservativeThanBH(t *testing.T) {
	if h := harmonic(4); math.Abs(h-(1+0.5+1.0/3+0.25)) > 1e-12 {
		t.Fatalf("harmonic(4) wrong: %v", h)
	}

	rng := rand.New(rand.NewPCG(41, 43))
	strictlyFewer := 0
	for trial := 0; trial < 100; trial++ {
		p := make([]float64, 300)
		for i := range p {
			p[i] = rng.Float64()
		}
		for i := 0; i < 60; i++ {
			p[i] = rng.Float64() * 0.02
		}
		_, bh := BH(p, 0.1)
		_, by := BY(p, 0.1)
		nbh, nby := countTrue(bh), countTrue(by)
		if nby > nbh {
			t.Fatalf("BY must never reject more than BH: %d vs %d", nby, nbh)
		}
		for i := range by {
			if by[i] && !bh[i] {
				t.Fatalf("BY's rejections must be a subset of BH's, index %d", i)
			}
		}
		if nbh == 0 {
			t.Fatal("the fixture should give BH something to reject")
		}
		if nby < nbh {
			strictlyFewer++
		}
	}
	if strictlyFewer == 0 {
		t.Fatal("BY should be strictly stricter than BH on at least some draws")
	}
}

func TestStoreyEstimatesTheNullFraction(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	m, signals := 4000, 800
	p := make([]float64, m)
	for i := range p {
		p[i] = rng.Float64()
	}
	for i := 0; i < signals; i++ {
		p[i] = rng.Float64() * 1e-6
	}
	pi0 := Storey(p, 0.5)
	want := float64(m-signals) / float64(m)
	if math.Abs(pi0-want) > 0.05 {
		t.Fatalf("pi0 should be near %.3f, got %.3f", want, pi0)
	}
	if Storey(nil, 0.5) != 1 {
		t.Fatal("with no data pi0 must fall back to 1")
	}

	_, bh := BH(p, 0.05)
	_, sbh := StoreyBH(p, 0.05, 0.5)
	if countTrue(sbh) < countTrue(bh) {
		t.Fatalf("adaptive BH must be at least as powerful: %d vs %d", countTrue(sbh), countTrue(bh))
	}
}

func TestGammaSequenceSumsToOne(t *testing.T) {
	var sum float64
	for j := 1; j <= 2000000; j++ {
		sum += Gamma(j)
	}
	if math.Abs(sum-1) > 1e-3 {
		t.Fatalf("the gamma sequence must sum to 1, got %v", sum)
	}
	for j := 2; j < 100; j++ {
		if Gamma(j) >= Gamma(j-1) {
			t.Fatalf("gamma must be non-increasing, broke at %d", j)
		}
	}
	if Gamma(0) != 0 {
		t.Fatal("gamma is defined from 1")
	}
}

func TestLORDControlsFDROnAStream(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 17))
	const n = 20000
	l := NewLORD(0.1)
	tp, fp := 0, 0
	for i := 0; i < n; i++ {
		signal := i%200 == 0
		p := rng.Float64()
		if signal {
			p *= 1e-6
		}
		if l.Step(p) {
			if signal {
				tp++
			} else {
				fp++
			}
		}
	}
	if tp+fp == 0 {
		t.Fatal("LORD rejected nothing at all")
	}
	got := float64(fp) / float64(tp+fp)
	if got > 0.15 {
		t.Fatalf("online FDR must stay near 0.1, got %.4f (%d true, %d false)", got, tp, fp)
	}
	if float64(tp)/float64(n/200) < 0.8 {
		t.Fatalf("strong signals should mostly be caught, got %d of %d", tp, n/200)
	}
	if l.Seen() != n || l.Rejections() != tp+fp {
		t.Fatalf("bookkeeping wrong: seen=%d rejections=%d", l.Seen(), l.Rejections())
	}
}

func TestLORDStaysQuietUnderTheNull(t *testing.T) {
	rng := rand.New(rand.NewPCG(19, 23))
	const n = 50000
	l := NewLORD(0.05)
	fired := 0
	for i := 0; i < n; i++ {
		if l.Step(rng.Float64()) {
			fired++
		}
	}
	if fired > 20 {
		t.Fatalf("a pure null stream should barely fire, got %d of %d", fired, n)
	}
}

func TestLORDLevelGrowsAfterARejection(t *testing.T) {
	l := NewLORD(0.1)
	for i := 0; i < 50; i++ {
		l.Step(0.9)
	}
	before := l.Level()
	l.Step(1e-9)
	after := l.Level()
	if after <= before {
		t.Fatalf("a rejection must refill the alpha wealth: %v then %v", before, after)
	}
	if after > 0.1 {
		t.Fatalf("the level must never exceed q, got %v", after)
	}
}

func TestOnlineHelpers(t *testing.T) {
	p := make([]float64, 100)
	for i := range p {
		p[i] = 0.9
	}
	p[50] = 1e-9
	if !Online(p, 0.1)[50] {
		t.Fatal("a tiny p-value must be discovered")
	}
	warm := OnlineFrom(p, 0.1, 60)
	if warm[50] {
		t.Fatal("nothing before the warm-up may fire")
	}
	if countTrue(OnlineFrom(p, 0.1, 200)) != 0 {
		t.Fatal("a warm-up past the end must yield no discoveries")
	}
}

func TestDegenerateInputs(t *testing.T) {
	if _, rej := BH(nil, 0.05); len(rej) != 0 {
		t.Fatal("no p-values means no decisions")
	}
	if _, rej := BH([]float64{0.01}, 0); countTrue(rej) != 0 {
		t.Fatal("q=0 must reject nothing")
	}
	withNaN := []float64{math.NaN(), 1e-9, math.NaN()}
	_, rej := BH(withNaN, 0.1)
	if len(rej) != 3 || !rej[1] || rej[0] || rej[2] {
		t.Fatalf("NaN p-values must be skipped, not shift the indices: %v", rej)
	}
	outOfRange := []float64{-1, 2, 1e-9}
	if _, rej := BH(outOfRange, 0.1); !rej[0] {
		t.Fatalf("a negative p-value must clamp to 0 and be rejected: %v", rej)
	}
}
