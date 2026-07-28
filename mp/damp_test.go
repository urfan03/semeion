package mp

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestMASSMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 5))
	series := make([]float64, 300)
	for i := range series {
		series[i] = math.Sin(float64(i)*0.21) + 0.2*rng.NormFloat64()
	}
	const m = 24
	query := series[100 : 100+m]

	got := MASS(query, series)
	if len(got) != len(series)-m+1 {
		t.Fatalf("expected %d distances, got %d", len(series)-m+1, len(got))
	}
	mu, sig := meanStd(series, m)
	var qs, qss float64
	for _, v := range query {
		qs += v
		qss += v * v
	}
	fm := float64(m)
	qMean := qs / fm
	qStd := math.Sqrt(qss/fm - qMean*qMean)
	for j := range got {
		var dot float64
		for k := 0; k < m; k++ {
			dot += query[k] * series[j+k]
		}
		want := dist(dot, qMean, mu[j], qStd, sig[j], fm)
		if math.Abs(got[j]-want) > 1e-6 {
			t.Fatalf("index %d: got %v want %v", j, got[j], want)
		}
	}
	if math.Abs(got[100]) > 1e-6 {
		t.Fatalf("a query must match itself at distance 0, got %v", got[100])
	}
}

func TestMASSDegenerate(t *testing.T) {
	if MASS([]float64{1}, []float64{1, 2, 3}) != nil {
		t.Fatal("a window below 2 has no distance profile")
	}
	if MASS([]float64{1, 2, 3, 4}, []float64{1, 2}) != nil {
		t.Fatal("a series shorter than the query has no distance profile")
	}
	flat := make([]float64, 100)
	d := MASS(flat[:10], flat)
	for i, v := range d {
		if v != 0 {
			t.Fatalf("constant against constant is distance 0, got %v at %d", v, i)
		}
	}
}

func TestSlidingDotMatchesDirect(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 13))
	series := make([]float64, 137)
	for i := range series {
		series[i] = rng.NormFloat64()
	}
	query := series[20:33]
	got := slidingDot(query, series)
	for j := range got {
		var want float64
		for k := range query {
			want += query[k] * series[j+k]
		}
		if math.Abs(got[j]-want) > 1e-8 {
			t.Fatalf("index %d: got %v want %v", j, got[j], want)
		}
	}
}

func TestDAMPFindsLateDiscordCausally(t *testing.T) {
	const n, m, at = 3000, 32, 2400
	rng := rand.New(rand.NewPCG(7, 9))
	ts := make([]float64, n)
	for i := range ts {
		ts[i] = math.Sin(float64(i)*0.3) + 0.05*rng.NormFloat64()
	}
	for k := 0; k < m; k++ {
		ts[at+k] = 6 * math.Sin(float64(k)*1.9)
	}

	scores := DAMP(ts, DAMPOptions{Window: m})
	if len(scores) != n {
		t.Fatalf("expected %d scores, got %d", n, len(scores))
	}
	best, bi := -1.0, -1
	for i, v := range scores {
		if v > best {
			best, bi = v, i
		}
	}
	if bi < at-m || bi > at+m {
		t.Fatalf("the discord is at %d, argmax landed at %d", at, bi)
	}
}

func TestDAMPIsCausal(t *testing.T) {
	const n, m, at = 1500, 24, 1200
	ts := make([]float64, n)
	for i := range ts {
		ts[i] = math.Sin(float64(i) * 0.4)
	}
	head := append([]float64(nil), ts[:at]...)

	full := DAMP(ts, DAMPOptions{Window: m, Warmup: 4 * m})
	prefix := DAMP(head, DAMPOptions{Window: m, Warmup: 4 * m})
	scored := at - m + 1
	for i := 0; i < scored; i++ {
		if math.Abs(full[i]-prefix[i]) > 1e-9 {
			t.Fatalf("appending future data changed the score at %d: %v vs %v", i, full[i], prefix[i])
		}
	}
	if full[scored] == 0 {
		t.Fatal("the longer series must score the subsequences the prefix could not")
	}
}

func TestDAMPAgreesWithLeftMatrixProfile(t *testing.T) {
	const n, m = 600, 20
	rng := rand.New(rand.NewPCG(21, 23))
	ts := make([]float64, n)
	for i := range ts {
		ts[i] = math.Sin(float64(i)*0.25) + 0.1*rng.NormFloat64()
	}
	for k := 0; k < m; k++ {
		ts[450+k] += 5
	}
	warmup := 4 * m
	damp := DAMP(ts, DAMPOptions{Window: m, Warmup: warmup})
	left := LeftMatrixProfile(ts, m)

	dArg, dBest := -1, -1.0
	lArg, lBest := -1, -1.0
	for i := warmup; i < len(left); i++ {
		if damp[i] > dBest {
			dBest, dArg = damp[i], i
		}
		if left[i] > lBest {
			lBest, lArg = left[i], i
		}
	}
	if dArg < lArg-m || dArg > lArg+m {
		t.Fatalf("DAMP discord at %d, left matrix profile at %d", dArg, lArg)
	}
}

func TestDAMPDegenerate(t *testing.T) {
	short := DAMP([]float64{1, 2, 3, 4}, DAMPOptions{Window: 8})
	if len(short) != 4 {
		t.Fatalf("output must match input length, got %d", len(short))
	}
	for _, v := range short {
		if v != 0 {
			t.Fatalf("too little data must score 0, got %v", v)
		}
	}
}

func BenchmarkDAMP(b *testing.B) {
	ts := randomSeries(4000, 4)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		DAMP(ts, DAMPOptions{Window: 16})
	}
}

func BenchmarkLeftMatrixProfile(b *testing.B) {
	ts := randomSeries(4000, 4)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		LeftMatrixProfile(ts, 16)
	}
}
