package selector

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"testing"
)

func TestExtractSeparatesSignalShapes(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	n := 1200

	seasonal := make([]float64, n)
	noisy := make([]float64, n)
	trending := make([]float64, n)
	flat := make([]float64, n)
	spiky := make([]float64, n)
	for i := 0; i < n; i++ {
		seasonal[i] = math.Sin(2 * math.Pi * float64(i) / 50)
		noisy[i] = rng.NormFloat64()
		trending[i] = 0.05 * float64(i)
		flat[i] = 4
		spiky[i] = rng.NormFloat64() * 0.1
	}
	for i := 100; i < n; i += 300 {
		spiky[i] = 20
	}

	fs := Extract(seasonal)
	fn := Extract(noisy)
	ft := Extract(trending)
	ff := Extract(flat)
	fp := Extract(spiky)

	if fs.Seasonality <= fn.Seasonality {
		t.Fatalf("a sine must look more seasonal than white noise: %.3f vs %.3f", fs.Seasonality, fn.Seasonality)
	}
	if period := math.Expm1(fs.LogPeriod); math.Abs(period-50) > 5 {
		t.Fatalf("the detected period should be near 50, got %.1f", period)
	}
	if ft.Trend <= fs.Trend {
		t.Fatalf("a ramp must look more trending than a sine: %.3f vs %.3f", ft.Trend, fs.Trend)
	}
	if fn.Noise <= fs.Noise {
		t.Fatalf("white noise must have a higher diff-to-level ratio: %.3f vs %.3f", fn.Noise, fs.Noise)
	}
	if ff.Flatness != 1 {
		t.Fatalf("a constant series is entirely flat, got %.3f", ff.Flatness)
	}
	if fp.Spikiness <= fn.Spikiness {
		t.Fatalf("injected spikes must raise spikiness: %.4f vs %.4f", fp.Spikiness, fn.Spikiness)
	}
	if len(fs.Vector()) != len(FeatureNames()) {
		t.Fatalf("vector and names must line up: %d vs %d", len(fs.Vector()), len(FeatureNames()))
	}
	for i, v := range fs.Vector() {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("feature %s is not finite: %v", FeatureNames()[i], v)
		}
	}
}

func TestExtractHandlesTinyInput(t *testing.T) {
	f := Extract([]float64{1, 2, 3})
	if f.LogLength <= 0 {
		t.Fatal("length must still be recorded")
	}
	for i, v := range f.Vector() {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("feature %s is not finite on tiny input: %v", FeatureNames()[i], v)
		}
	}
}

func TestModelPredictsByNeighbourhood(t *testing.T) {
	m := New(1, "fallback")
	if got := m.Predict(Features{}); got != "fallback" {
		t.Fatalf("an empty model must return the fallback, got %q", got)
	}

	seasonalLike := Features{LogLength: 7, Seasonality: 0.9, LogPeriod: 4}
	noiseLike := Features{LogLength: 7, Seasonality: 0.05, LogPeriod: 1}
	m.Add("a", seasonalLike, "mp")
	m.Add("b", Features{LogLength: 7, Seasonality: 0.85, LogPeriod: 3.9}, "mp")
	m.Add("c", noiseLike, "evt")
	m.Add("d", Features{LogLength: 7, Seasonality: 0.1, LogPeriod: 1.1}, "evt")
	m.Fit()

	if got := m.Predict(Features{LogLength: 7, Seasonality: 0.88, LogPeriod: 3.95}); got != "mp" {
		t.Fatalf("a seasonal query should pick mp, got %q", got)
	}
	if got := m.Predict(Features{LogLength: 7, Seasonality: 0.07, LogPeriod: 1.05}); got != "evt" {
		t.Fatalf("a noisy query should pick evt, got %q", got)
	}
}

func TestModelWithoutIsLeaveOneOut(t *testing.T) {
	m := New(1, "fallback")
	m.Add("a", Features{Seasonality: 0.9}, "mp")
	m.Add("b", Features{Seasonality: 0.1}, "evt")
	m.Fit()

	loo := m.Without("a")
	if len(loo.Examples) != 1 || loo.Examples[0].Key != "b" {
		t.Fatalf("Without must drop exactly the named series: %+v", loo.Examples)
	}
	if got := loo.Predict(Features{Seasonality: 0.9}); got != "evt" {
		t.Fatalf("with a held out, only evt remains to vote, got %q", got)
	}
	if len(m.Examples) != 2 {
		t.Fatal("Without must not mutate the original model")
	}
}

func TestModelRoundTripsThroughJSON(t *testing.T) {
	m := New(2, "evt")
	m.Add("a", Features{LogLength: 8, Seasonality: 0.7}, "mp")
	m.Add("b", Features{LogLength: 8, Seasonality: 0.2}, "hst")
	m.Fit()

	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var back Model
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.K != 2 || back.Fallback != "evt" || len(back.Examples) != 2 {
		t.Fatalf("model did not round-trip: %+v", back)
	}
	q := Features{LogLength: 8, Seasonality: 0.68}
	if m.Predict(q) != back.Predict(q) {
		t.Fatalf("predictions diverged after a round trip: %q vs %q", m.Predict(q), back.Predict(q))
	}
}

func TestModelIsDeterministicOnTies(t *testing.T) {
	m := New(2, "z")
	m.Add("a", Features{Seasonality: 1}, "alpha")
	m.Add("b", Features{Seasonality: 1}, "beta")
	m.Fit()
	first := m.Predict(Features{Seasonality: 1})
	for i := 0; i < 20; i++ {
		if got := m.Predict(Features{Seasonality: 1}); got != first {
			t.Fatalf("a tie must resolve the same way every time: %q vs %q", got, first)
		}
	}
}
