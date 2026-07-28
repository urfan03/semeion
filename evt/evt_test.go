package evt

import (
	"math"
	"math/rand/v2"
	"testing"
)

func gpdSample(rng *rand.Rand, gamma, sigma float64) float64 {
	u := rng.Float64()
	if u <= 0 {
		u = 1e-12
	}
	if math.Abs(gamma) < 1e-12 {
		return -sigma * math.Log(u)
	}
	return sigma / gamma * (math.Pow(u, -gamma) - 1)
}

func TestFitGPDRecoversParameters(t *testing.T) {
	cases := []struct{ gamma, sigma float64 }{
		{0.0, 2.0},
		{0.3, 1.5},
		{-0.2, 1.0},
	}
	for _, c := range cases {
		rng := rand.New(rand.NewPCG(uint64(len(cases)), 99))
		peaks := make([]float64, 20000)
		for i := range peaks {
			peaks[i] = gpdSample(rng, c.gamma, c.sigma)
		}
		g, ok := FitGPD(peaks)
		if !ok {
			t.Fatalf("gamma=%v sigma=%v: fit failed", c.gamma, c.sigma)
		}
		if math.Abs(g.Gamma-c.gamma) > 0.08 {
			t.Fatalf("gamma=%v: got %v", c.gamma, g.Gamma)
		}
		if math.Abs(g.Sigma-c.sigma)/c.sigma > 0.12 {
			t.Fatalf("sigma=%v: got %v", c.sigma, g.Sigma)
		}
	}
}

func TestFitGPDRejectsTinySamples(t *testing.T) {
	if _, ok := FitGPD([]float64{1, 2, 3}); ok {
		t.Fatal("fit must fail below the minimum peak count")
	}
	if _, ok := FitGPD([]float64{0, -1, -2, -3, -4, -5, -6}); ok {
		t.Fatal("fit must fail when no positive peaks remain")
	}
}

func TestPOTThresholdExceedsData(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	data := make([]float64, 5000)
	max := math.Inf(-1)
	for i := range data {
		data[i] = rng.NormFloat64()
		if data[i] > max {
			max = data[i]
		}
	}
	z, g, ok := POT(data, Options{Q: 1e-5, Level: 0.98})
	if !ok {
		t.Fatal("POT should converge on 5000 gaussian samples")
	}
	if z <= max {
		t.Fatalf("q=1e-5 threshold should sit above the observed max: z=%.3f max=%.3f", z, max)
	}
	if g.Sigma <= 0 {
		t.Fatalf("sigma must be positive: %v", g.Sigma)
	}

	zLoose, _, _ := POT(data, Options{Q: 1e-2, Level: 0.98})
	if zLoose >= z {
		t.Fatalf("a looser q must give a lower threshold: %.3f vs %.3f", zLoose, z)
	}
}

func TestSPOTAlarmsOnExtremesNotNoise(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 13))
	n := 6000
	vals := make([]float64, n)
	for i := range vals {
		vals[i] = rng.NormFloat64()
	}
	spikes := []int{3000, 4500}
	for _, i := range spikes {
		vals[i] = 25
	}
	alarms, thr := Stream(vals, StreamOptions{Options: Options{Q: 1e-4, Level: 0.98}, Calibration: 2000})
	for _, i := range spikes {
		if !alarms[i] {
			t.Fatalf("spike at %d must alarm (threshold %.3f)", i, thr[i])
		}
	}
	fp := 0
	for i, a := range alarms {
		if a && i != spikes[0] && i != spikes[1] {
			fp++
		}
	}
	if fp > 5 {
		t.Fatalf("too many false alarms on gaussian noise: %d", fp)
	}
}

func TestDSPOTFollowsDrift(t *testing.T) {
	rng := rand.New(rand.NewPCG(21, 23))
	n := 6000
	vals := make([]float64, n)
	for i := range vals {
		vals[i] = 0.01*float64(i) + rng.NormFloat64()
	}
	spike := 5000
	vals[spike] += 30

	plain, _ := Stream(vals, StreamOptions{Options: Options{Q: 1e-4, Level: 0.98}, Calibration: 2000})
	drift, _ := Stream(vals, StreamOptions{Options: Options{Q: 1e-4, Level: 0.98}, Calibration: 2000, Depth: 20, Drift: true})

	if !drift[spike] {
		t.Fatal("DSPOT must catch the spike on a drifting series")
	}
	countPlain, countDrift := 0, 0
	for i := range vals {
		if i == spike {
			continue
		}
		if plain[i] {
			countPlain++
		}
		if drift[i] {
			countDrift++
		}
	}
	if countDrift >= countPlain {
		t.Fatalf("drift model should suppress trend-driven alarms: dspot=%d spot=%d", countDrift, countPlain)
	}
}

func TestSurvivalIsMonotoneAndBounded(t *testing.T) {
	g := GPD{Gamma: 0.25, Sigma: 2}
	if p := Survival(g, 5, 4, 1000, 20); p != 1 {
		t.Fatalf("below the initial threshold the tail model says nothing: %v", p)
	}
	near := Survival(g, 5, 6, 1000, 20)
	far := Survival(g, 5, 60, 1000, 20)
	if far >= near {
		t.Fatalf("further into the tail must be less probable: %v vs %v", far, near)
	}
	if near > float64(20)/float64(1000) {
		t.Fatalf("tail probability cannot exceed the exceedance rate: %v", near)
	}
	if far < 0 {
		t.Fatalf("probability must stay non-negative: %v", far)
	}
	exp := Survival(GPD{Gamma: 0, Sigma: 2}, 5, 9, 1000, 20)
	want := 0.02 * math.Exp(-2)
	if math.Abs(exp-want) > 1e-12 {
		t.Fatalf("gamma=0 must fall back to the exponential tail: %v want %v", exp, want)
	}
}

func TestStreamProbabilitiesRanksExtremes(t *testing.T) {
	rng := rand.New(rand.NewPCG(31, 37))
	n := 5000
	vals := make([]float64, n)
	for i := range vals {
		vals[i] = rng.NormFloat64()
	}
	vals[4000] = 18
	vals[4500] = -18

	opt := StreamOptions{Calibration: 2000}
	p := StreamProbabilities(vals, opt)
	if len(p) != n {
		t.Fatalf("expected %d probabilities, got %d", n, len(p))
	}
	for i := 0; i < opt.Calibration; i++ {
		if p[i] != 1 {
			t.Fatalf("calibration window must stay uninformative, got %v at %d", p[i], i)
		}
	}
	if p[4000] >= 1e-4 {
		t.Fatalf("a huge positive spike needs a tiny p-value, got %v", p[4000])
	}
	if p[4500] < 0.5 {
		t.Fatalf("an upper-tail model must stay silent on the negative spike, got %v", p[4500])
	}

	two := TwoSidedProbabilities(vals, opt)
	if two[4500] >= 1e-3 {
		t.Fatalf("the two-sided model must catch the drop, got %v", two[4500])
	}
	if two[4000] >= 1e-3 {
		t.Fatalf("the two-sided model must still catch the spike, got %v", two[4000])
	}
	for i, v := range two {
		if v <= 0 || v > 1 {
			t.Fatalf("p-value out of range at %d: %v", i, v)
		}
	}

	scores := Scores(vals, opt)
	if scores[4000] <= scores[100] {
		t.Fatalf("-log10(p) must rank the spike above the calibration bulk: %v vs %v", scores[4000], scores[100])
	}
}

func TestStreamProbabilitiesShortInput(t *testing.T) {
	p := StreamProbabilities([]float64{1, 2, 3}, StreamOptions{Calibration: 100})
	if len(p) != 3 {
		t.Fatalf("expected 3 values, got %d", len(p))
	}
	for _, v := range p {
		if v != 1 {
			t.Fatalf("too little data must yield no evidence, got %v", v)
		}
	}
}

func TestQuantileMonotoneInQ(t *testing.T) {
	g := GPD{Gamma: 0.2, Sigma: 1.5}
	a := Quantile(g, 3, 1e-3, 10000, 200)
	b := Quantile(g, 3, 1e-5, 10000, 200)
	if b <= a {
		t.Fatalf("smaller q must give a higher quantile: %.3f vs %.3f", b, a)
	}
	if Quantile(g, 3, 0, 0, 0) != 3 {
		t.Fatal("degenerate inputs must fall back to the initial threshold")
	}
}
