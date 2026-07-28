package shape

import (
	"math"
	"math/rand/v2"
	"testing"
)

func flatWithNoise(n int, level, noise float64, seed uint64) []float64 {
	rng := rand.New(rand.NewPCG(seed, seed^0x33))
	out := make([]float64, n)
	for i := range out {
		out[i] = level + noise*rng.NormFloat64()
	}
	return out
}

func TestClassifySpikeAndDip(t *testing.T) {
	v := flatWithNoise(400, 100, 1, 1)
	for i := 200; i < 205; i++ {
		v[i] = 160
	}
	got := Classify(v, 200, 204, Options{})
	if got.Kind != Spike {
		t.Fatalf("expected a spike, got %+v", got)
	}
	if got.Z < 10 || got.Duration != 5 {
		t.Fatalf("spike stats wrong: %+v", got)
	}
	if !Transient(got.Kind) || Persistent(got.Kind) {
		t.Fatal("a spike is transient, not persistent")
	}

	d := flatWithNoise(400, 100, 1, 2)
	for i := 200; i < 205; i++ {
		d[i] = 40
	}
	if k := Classify(d, 200, 204, Options{}).Kind; k != Dip {
		t.Fatalf("expected a dip, got %v", k)
	}
}

func TestClassifyLevelShift(t *testing.T) {
	v := flatWithNoise(400, 100, 1, 3)
	for i := 200; i < 400; i++ {
		v[i] += 40
	}
	got := Classify(v, 200, 210, Options{})
	if got.Kind != LevelUp {
		t.Fatalf("expected a level shift up, got %+v", got)
	}
	if !Persistent(got.Kind) || Transient(got.Kind) {
		t.Fatal("a level shift is persistent, not transient")
	}
	if math.Abs(got.After-got.During) > 10 {
		t.Fatalf("after and during should agree on a level shift: %+v", got)
	}

	down := flatWithNoise(400, 100, 1, 4)
	for i := 200; i < 400; i++ {
		down[i] -= 40
	}
	if k := Classify(down, 200, 210, Options{}).Kind; k != LevelDown {
		t.Fatalf("expected a level shift down, got %v", k)
	}
}

func TestClassifyVarianceChange(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 7))
	v := flatWithNoise(500, 100, 1, 5)
	for i := 200; i < 260; i++ {
		v[i] = 100 + 12*rng.NormFloat64()
	}
	got := Classify(v, 200, 259, Options{})
	if got.Kind != Variance {
		t.Fatalf("expected a variance change, got %+v", got)
	}
	if got.Spread < 2 {
		t.Fatalf("the spread ratio should be large, got %v", got.Spread)
	}
}

func TestClassifyGap(t *testing.T) {
	v := flatWithNoise(400, 100, 1, 9)
	for i := 200; i < 240; i++ {
		v[i] = 0
	}
	got := Classify(v, 200, 239, Options{})
	if got.Kind != Gap {
		t.Fatalf("a run of identical far-from-baseline values is a gap, got %+v", got)
	}
	if !Persistent(got.Kind) {
		t.Fatal("a gap is persistent")
	}
}

func TestClassifyTrendBreak(t *testing.T) {
	v := flatWithNoise(600, 100, 1, 11)
	for i := 300; i < 600; i++ {
		v[i] += 0.3 * float64(i-300)
	}
	got := Classify(v, 298, 302, Options{Context: 200})
	if got.Kind != TrendBreak {
		t.Fatalf("expected a trend break, got %+v", got)
	}
	if math.Abs(got.Slope) < 0.1 {
		t.Fatalf("the slope change should be visible, got %v", got.Slope)
	}
}

func TestClassifyDegenerate(t *testing.T) {
	v := flatWithNoise(100, 10, 1, 13)
	if k := Classify(v, -1, 5, Options{}).Kind; k != Unknown {
		t.Fatalf("a negative start must be unknown, got %v", k)
	}
	if k := Classify(v, 5, 200, Options{}).Kind; k != Unknown {
		t.Fatalf("an end past the series must be unknown, got %v", k)
	}
	if k := Classify(v, 10, 5, Options{}).Kind; k != Unknown {
		t.Fatalf("an inverted range must be unknown, got %v", k)
	}
	if got := Classify(v, 1, 2, Options{}); got.Kind != Unknown || got.Duration != 2 {
		t.Fatalf("too little context must be unknown but still report duration: %+v", got)
	}

	flat := make([]float64, 200)
	if k := Classify(flat, 100, 105, Options{}).Kind; k != Unknown {
		t.Fatalf("a constant series has no anomaly shape, got %v", k)
	}
}

func TestClassifyQuietDeviationIsUnknown(t *testing.T) {
	v := flatWithNoise(400, 100, 5, 17)
	for i := 200; i < 205; i++ {
		v[i] += 2
	}
	if k := Classify(v, 200, 204, Options{}).Kind; k != Unknown {
		t.Fatalf("a deviation inside the noise band must not be classified, got %v", k)
	}
}

func TestSpikeFilterRaisesPrecisionOnNoisySeries(t *testing.T) {
	rng := rand.New(rand.NewPCG(21, 23))
	n := 4000
	v := flatWithNoise(n, 100, 3, 21)
	realAt := []int{1000, 2000, 3000}
	for _, at := range realAt {
		for i := at; i < at+40; i++ {
			v[i] += 30
		}
	}
	var noise []int
	for i := 500; i < n-100; i += 137 {
		v[i] += 15 + 5*rng.Float64()
		noise = append(noise, i)
	}

	keep := func(r Result) bool {
		return r.Kind != Unknown && r.Duration >= 5 && math.Abs(r.Z) >= 5
	}
	real, dropped := 0, 0
	for _, at := range realAt {
		if keep(Classify(v, at, at+39, Options{Context: 200})) {
			real++
		}
	}
	for _, at := range noise {
		if !keep(Classify(v, at, at, Options{Context: 200})) {
			dropped++
		}
	}
	if real < 3 {
		t.Fatalf("all three sustained events must survive the duration+magnitude filter, got %d", real)
	}
	if dropped != len(noise) {
		t.Fatalf("every single-point wobble must be dropped: dropped %d of %d", dropped, len(noise))
	}
}
