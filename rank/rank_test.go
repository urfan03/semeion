package rank

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"testing"
)

func synthetic(n int, seed uint64) []Example {
	rng := rand.New(rand.NewPCG(seed, seed^0x2a))
	out := make([]Example, 0, n)
	for i := 0; i < n; i++ {
		real := rng.Float64() < 0.3
		f := Features{
			LogScore:    rng.NormFloat64(),
			Effect:      math.Abs(rng.NormFloat64()),
			LogDuration: math.Abs(rng.NormFloat64()),
			Agreement:   rng.Float64(),
			Noise:       rng.Float64(),
			ChangeNear:  0,
			PeerBacked:  0,
		}
		if real {
			f.LogScore += 2
			f.Effect += 3
			f.LogDuration += 1.5
			f.Agreement = 0.7 + 0.3*rng.Float64()
			f.Persistent = 1
			f.PeerBacked = 1
			f.Noise *= 0.3
		}
		out = append(out, Example{Features: f, Real: real})
	}
	return out
}

func accuracy(m *Model, examples []Example, cut float64) (precision, recall float64) {
	tp, fp, positives := 0, 0, 0
	for _, ex := range examples {
		if ex.Real {
			positives++
		}
		if m.Score(ex.Features) < cut {
			continue
		}
		if ex.Real {
			tp++
		} else {
			fp++
		}
	}
	if tp+fp > 0 {
		precision = float64(tp) / float64(tp+fp)
	}
	if positives > 0 {
		recall = float64(tp) / float64(positives)
	}
	return precision, recall
}

func TestModelLearnsFromFeedback(t *testing.T) {
	train := synthetic(2000, 1)
	test := synthetic(2000, 2)

	m := New()
	before, _ := accuracy(m, test, 0.5)
	m.Fit(train, 5)
	after, recall := accuracy(m, test, 0.5)

	if after <= before {
		t.Fatalf("training must improve precision: %.4f then %.4f", before, after)
	}
	if after < 0.8 {
		t.Fatalf("this fixture is separable; expected >=80%% precision, got %.4f", after)
	}
	if recall < 0.5 {
		t.Fatalf("and it must not achieve that by refusing to fire, recall %.4f", recall)
	}
	if m.Seen != len(train)*5 {
		t.Fatalf("Seen must count every update, got %d", m.Seen)
	}
}

func TestMonotonicityIsEnforced(t *testing.T) {
	m := New()
	flipped := make([]Example, 0, 400)
	for _, ex := range synthetic(400, 3) {
		ex.Real = !ex.Real
		flipped = append(flipped, ex)
	}
	m.Fit(flipped, 10)

	w := m.Weights()
	for i, name := range Names() {
		d := Directions()[i]
		if d > 0 && w[name] < 0 {
			t.Fatalf("%s must never learn a negative weight, got %v", name, w[name])
		}
		if d < 0 && w[name] > 0 {
			t.Fatalf("%s must never learn a positive weight, got %v", name, w[name])
		}
	}
}

func TestScoreIsMonotoneInEvidence(t *testing.T) {
	m := New()
	m.Fit(synthetic(2000, 5), 5)

	base := Features{LogScore: 0, Effect: 1, LogDuration: 1, Agreement: 0.5}
	stronger := base
	stronger.Effect = 8
	if m.Score(stronger) <= m.Score(base) {
		t.Fatalf("a larger effect must raise the score: %.4f vs %.4f", m.Score(stronger), m.Score(base))
	}
	longer := base
	longer.LogDuration = 5
	if m.Score(longer) <= m.Score(base) {
		t.Fatalf("a longer anomaly must raise the score: %.4f vs %.4f", m.Score(longer), m.Score(base))
	}
	noisier := base
	noisier.Noise = 5
	if m.Score(noisier) > m.Score(base) {
		t.Fatalf("more noise must not raise the score: %.4f vs %.4f", m.Score(noisier), m.Score(base))
	}
	for _, f := range []Features{base, stronger, longer, noisier} {
		if s := m.Score(f); s < 0 || s > 1 {
			t.Fatalf("score out of range: %v", s)
		}
	}
}

func TestScoreIgnoresNonFiniteFeatures(t *testing.T) {
	m := New()
	m.Fit(synthetic(500, 7), 3)
	bad := Features{LogScore: math.NaN(), Effect: math.Inf(1), LogDuration: 2}
	s := m.Score(bad)
	if math.IsNaN(s) || s < 0 || s > 1 {
		t.Fatalf("a non-finite feature must not poison the score, got %v", s)
	}
	m.Learn(bad, true)
	for i, w := range m.W {
		if math.IsNaN(w) || math.IsInf(w, 0) {
			t.Fatalf("weight %d became non-finite: %v", i, w)
		}
	}
}

func TestThresholdHoldsAFalseRateBudget(t *testing.T) {
	train := synthetic(2000, 11)
	test := synthetic(2000, 13)
	m := New()
	m.Fit(train, 5)

	cut, err := m.Threshold(train, 0.2)
	if err != nil {
		t.Fatal(err)
	}
	precision, recall := accuracy(m, test, cut)
	if precision < 0.75 {
		t.Fatalf("a 20%% false-rate budget should hold on held-out data, got precision %.4f", precision)
	}
	if recall <= 0 {
		t.Fatal("the calibrated cut must still let something through")
	}

	if _, err := m.Threshold(nil, 0.2); err == nil {
		t.Fatal("calibrating against nothing must error")
	}
	impossible := make([]Example, 50)
	for i := range impossible {
		impossible[i] = Example{Features: Features{}, Real: false}
	}
	if _, err := m.Threshold(impossible, 0.01); err == nil {
		t.Fatal("an unreachable budget must error rather than silently return a useless cut")
	}
}

func TestModelRoundTripsThroughJSON(t *testing.T) {
	m := New()
	m.Fit(synthetic(500, 17), 3)
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var back Model
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	f := Features{LogScore: 1, Effect: 4, LogDuration: 2, Agreement: 0.8}
	if math.Abs(m.Score(f)-back.Score(f)) > 1e-12 {
		t.Fatalf("scores diverged after a round trip: %v vs %v", m.Score(f), back.Score(f))
	}
	if back.Seen != m.Seen {
		t.Fatalf("Seen did not survive: %d vs %d", back.Seen, m.Seen)
	}

	var empty Model
	if err := json.Unmarshal([]byte(`{}`), &empty); err != nil {
		t.Fatal(err)
	}
	if len(empty.W) != len(Names()) || empty.Rate <= 0 {
		t.Fatalf("an empty model must resolve to usable defaults: %+v", empty)
	}
	if s := empty.Score(f); s < 0 || s > 1 {
		t.Fatalf("and must still score: %v", s)
	}
}

func TestFeatureVectorMatchesNames(t *testing.T) {
	f := Features{LogScore: 1, Effect: 2, LogDuration: 3, Agreement: 4,
		Persistent: 5, Seasonality: 6, Noise: 7, ChangeNear: 8, PeerBacked: 9}
	v := f.Vector()
	if len(v) != len(Names()) || len(v) != len(Directions()) {
		t.Fatalf("vector, names and directions must line up: %d %d %d", len(v), len(Names()), len(Directions()))
	}
	for i, want := range []float64{1, 2, 3, 4, 5, 6, 7, 8, 9} {
		if v[i] != want {
			t.Fatalf("vector order wrong at %d (%s): %v", i, Names()[i], v[i])
		}
	}
}
