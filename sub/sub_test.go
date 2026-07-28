package sub

import (
	"math"
	"math/rand/v2"
	"testing"
)

func seriesWithBurst(n, at, width int, seed uint64) []float64 {
	rng := rand.New(rand.NewPCG(seed, seed^0xfeed))
	out := make([]float64, n)
	for i := range out {
		out[i] = 10 + 2*math.Sin(float64(i)*0.2) + 0.08*rng.NormFloat64()
	}
	for i := at; i < at+width && i < n; i++ {
		out[i] += 9
	}
	return out
}

func peakInside(scores []float64, at, width int) (inside, outside float64) {
	for i, v := range scores {
		if i >= at-width && i < at+2*width {
			if v > inside {
				inside = v
			}
		} else if v > outside {
			outside = v
		}
	}
	return inside, outside
}

func TestEmbedAndScatter(t *testing.T) {
	e := Embed([]float64{1, 2, 3, 4, 5}, 3, 1)
	if len(e.Rows) != 3 || e.Rows[1][0] != 2 || e.Starts[2] != 2 {
		t.Fatalf("unexpected embedding: %+v", e)
	}
	strided := Embed([]float64{1, 2, 3, 4, 5, 6, 7}, 3, 2)
	if len(strided.Rows) != 3 || strided.Starts[1] != 2 {
		t.Fatalf("stride 2 should land on 0,2,4: %+v", strided.Starts)
	}
	if got := Embed([]float64{1, 2}, 5, 1); got.Rows != nil {
		t.Fatal("a window longer than the series has no rows")
	}

	point := e.Scatter([]float64{1, 5, 2}, false)
	if point[1] != 5 || point[3] != 0 {
		t.Fatalf("unspread scatter must land on window starts: %v", point)
	}
	spread := e.Scatter([]float64{1, 5, 2}, true)
	if spread[3] != 5 || spread[0] != 1 {
		t.Fatalf("spread scatter must cover the window: %v", spread)
	}
	if bad := e.Scatter([]float64{1}, false); len(bad) != 5 || bad[0] != 0 {
		t.Fatalf("a length mismatch must yield zeros, got %v", bad)
	}
}

func TestZNormalize(t *testing.T) {
	e := Embed([]float64{1, 2, 3, 10, 20, 30}, 3, 3).ZNormalize()
	for _, row := range e.Rows {
		var sum, ss float64
		for _, v := range row {
			sum += v
			ss += v * v
		}
		if math.Abs(sum) > 1e-9 {
			t.Fatalf("z-normalized rows must be zero-mean, got %v", sum)
		}
		if math.Abs(ss/float64(len(row))-1) > 1e-9 {
			t.Fatalf("z-normalized rows must have unit variance, got %v", ss/float64(len(row)))
		}
	}
	flat := Embed([]float64{5, 5, 5, 5}, 4, 1).ZNormalize()
	for _, v := range flat.Rows[0] {
		if v != 0 {
			t.Fatalf("a constant window must normalize to zeros, got %v", v)
		}
	}
}

func TestKNNAndLOFFlagTheBurst(t *testing.T) {
	const n, at, width = 1500, 1000, 30
	ts := seriesWithBurst(n, at, width, 3)
	opt := Options{Window: 16, K: 5}

	for name, scores := range map[string][]float64{"knn": KNN(ts, opt), "lof": LOF(ts, opt)} {
		if len(scores) != n {
			t.Fatalf("%s: expected %d scores, got %d", name, n, len(scores))
		}
		inside, outside := peakInside(scores, at, width)
		if inside <= outside {
			t.Fatalf("%s: the burst must score above everything else: %.4f vs %.4f", name, inside, outside)
		}
	}
}

func TestPCAFlagsTheBurst(t *testing.T) {
	const n, at, width = 1500, 900, 30
	ts := seriesWithBurst(n, at, width, 5)
	scores := PCA(ts, PCAOptions{Options: Options{Window: 16}, Variance: 0.9})
	if len(scores) != n {
		t.Fatalf("expected %d scores, got %d", n, len(scores))
	}
	inside, outside := peakInside(scores, at, width)
	if inside <= outside {
		t.Fatalf("PCA reconstruction error must peak on the burst: %.4f vs %.4f", inside, outside)
	}

	fixed := PCA(ts, PCAOptions{Options: Options{Window: 16}, Components: 2})
	full := PCA(ts, PCAOptions{Options: Options{Window: 16}, Components: 16})
	var fixedSum, fullSum float64
	for i := range fixed {
		fixedSum += fixed[i]
		fullSum += full[i]
	}
	if fullSum >= fixedSum {
		t.Fatalf("keeping every component must drive reconstruction error to ~0: %v vs %v", fullSum, fixedSum)
	}
}

func TestJacobiEigenDecomposes(t *testing.T) {
	a := [][]float64{
		{4, 1, 0},
		{1, 3, 1},
		{0, 1, 2},
	}
	vals, vecs := jacobiEigen(a)
	for i := 1; i < len(vals); i++ {
		if vals[i] > vals[i-1] {
			t.Fatalf("eigenvalues must come out descending: %v", vals)
		}
	}
	var trace float64
	for i := range a {
		trace += a[i][i]
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	if math.Abs(sum-trace) > 1e-9 {
		t.Fatalf("eigenvalues must sum to the trace: %v vs %v", sum, trace)
	}
	for c, vec := range vecs {
		for r := range a {
			var got float64
			for k := range a {
				got += a[r][k] * vec[k]
			}
			if math.Abs(got-vals[c]*vec[r]) > 1e-8 {
				t.Fatalf("A·v must equal λ·v for pair %d row %d: %v vs %v", c, r, got, vals[c]*vec[r])
			}
		}
		var norm float64
		for _, v := range vec {
			norm += v * v
		}
		if math.Abs(norm-1) > 1e-9 {
			t.Fatalf("eigenvectors must be unit length, got %v", norm)
		}
	}
}

func TestIForestFlagsTheBurst(t *testing.T) {
	const n, at, width = 1500, 800, 30
	ts := seriesWithBurst(n, at, width, 7)
	scores := IForest(ts, ForestOptions{Options: Options{Window: 16}, Trees: 60, SampleSize: 128, Seed: 4})
	if len(scores) != n {
		t.Fatalf("expected %d scores, got %d", n, len(scores))
	}
	inside, outside := peakInside(scores, at, width)
	if inside <= outside {
		t.Fatalf("isolation forest must isolate the burst first: %.4f vs %.4f", inside, outside)
	}
	for i, v := range scores {
		if v < 0 || v > 1 {
			t.Fatalf("isolation scores must stay in [0,1], got %v at %d", v, i)
		}
	}

	again := IForest(ts, ForestOptions{Options: Options{Window: 16}, Trees: 60, SampleSize: 128, Seed: 4})
	for i := range scores {
		if scores[i] != again[i] {
			t.Fatalf("the same seed must give identical scores, diverged at %d", i)
		}
	}
}

func TestPopulationReusesOutlierDetector(t *testing.T) {
	const n, at, width = 600, 400, 20
	ts := seriesWithBurst(n, at, width, 11)
	scores, err := Population(ts, Options{Window: 16, Stride: 4, K: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != n {
		t.Fatalf("expected %d scores, got %d", n, len(scores))
	}
	inside, outside := peakInside(scores, at, width)
	if inside <= outside {
		t.Fatalf("the population detector must rank the burst highest: %.4f vs %.4f", inside, outside)
	}
}

func TestDetectorsHandleShortInput(t *testing.T) {
	short := []float64{1, 2, 3}
	for name, got := range map[string][]float64{
		"knn":     KNN(short, Options{Window: 8}),
		"lof":     LOF(short, Options{Window: 8}),
		"pca":     PCA(short, PCAOptions{Options: Options{Window: 8}}),
		"iforest": IForest(short, ForestOptions{Options: Options{Window: 8}}),
	} {
		if len(got) != len(short) {
			t.Fatalf("%s: length must match the input, got %d", name, len(got))
		}
		for _, v := range got {
			if v != 0 {
				t.Fatalf("%s: too little data must score 0, got %v", name, v)
			}
		}
	}
	if got := AutoWindow(3); got != 0 {
		t.Fatalf("a series too short for any window must give 0, got %d", got)
	}
	if got := AutoWindow(4000); got != 16 {
		t.Fatalf("expected the default window, got %d", got)
	}
}
