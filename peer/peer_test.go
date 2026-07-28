package peer

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/urfan03/semeion/fuse"
)

func fleet(m, n int, seed uint64) [][]float64 {
	rng := rand.New(rand.NewPCG(seed, seed^0x5a))
	rows := make([][]float64, m)
	shared := make([]float64, n)
	for i := range shared {
		shared[i] = 100 + 20*math.Sin(2*math.Pi*float64(i)/144)
	}
	for s := range rows {
		row := make([]float64, n)
		gain := 0.8 + 0.4*rng.Float64()
		for i := range row {
			row[i] = gain*shared[i] + rng.NormFloat64()
		}
		rows[s] = row
	}
	return rows
}

func TestNormalizeIsCausalAndRobust(t *testing.T) {
	rows := fleet(3, 600, 1)
	rows[0][400] = 500

	z := Normalize(rows, 100)
	if len(z) != 3 || len(z[0]) != 600 {
		t.Fatalf("shape wrong: %d %d", len(z), len(z[0]))
	}
	if z[0][400] < 20 {
		t.Fatalf("a huge spike must show a large robust z, got %v", z[0][400])
	}
	if math.Abs(z[0][401]) > 20 {
		t.Fatalf("a median baseline must not be dragged by one outlier, got %v", z[0][401])
	}

	prefix := Normalize([][]float64{rows[0][:300]}, 100)
	for i := 0; i < 300; i++ {
		if math.Abs(z[0][i]-prefix[0][i]) > 1e-9 {
			t.Fatalf("normalization must be causal, diverged at %d", i)
		}
	}
	for i := 0; i < 4; i++ {
		if z[0][i] != 0 {
			t.Fatalf("no baseline yet at %d, must stay 0", i)
		}
	}
}

func TestRelativeIgnoresTrafficSurgesButNotSingleFaults(t *testing.T) {
	n := 600
	rows := fleet(8, n, 3)
	surge := 300
	for s := range rows {
		rows[s][surge] *= 2.5
	}
	fault := 450
	rows[2][fault] *= 2.5

	scores := Scores(rows, Options{Window: 100})
	if len(scores) != 8 {
		t.Fatalf("expected 8 rows, got %d", len(scores))
	}

	var maxSurge float64
	for s := range scores {
		if math.Abs(scores[s][surge]) > maxSurge {
			maxSurge = math.Abs(scores[s][surge])
		}
	}
	if maxSurge >= math.Abs(scores[2][fault]) {
		t.Fatalf("a fleet-wide surge must score below a single-instance fault: %.3f vs %.3f",
			maxSurge, math.Abs(scores[2][fault]))
	}
	if math.Abs(scores[2][fault]) < 5 {
		t.Fatalf("the lone fault must stand out, got %.3f", scores[2][fault])
	}
}

func TestDeviationSpotsCrossSectionalOutliers(t *testing.T) {
	rows := [][]float64{
		{1, 1, 1, 1},
		{1, 1, 1, 1},
		{1, 1, 1, 1},
		{1, 1, 1, 9},
	}
	out := Deviation(rows, 2)
	if out[3][3] <= out[0][3] {
		t.Fatalf("the deviating series must score highest: %v vs %v", out[3][3], out[0][3])
	}
	if out[0][0] != 0 {
		t.Fatalf("an in-line series must score 0, got %v", out[0][0])
	}
}

func TestDeviationNeedsEnoughPeers(t *testing.T) {
	rows := fleet(1, 300, 5)
	if out := Deviation(rows, 2); len(out) != 1 || out[0][100] != 0 {
		t.Fatal("a single series has no peers to compare against")
	}
	if out := Deviation(nil, 2); len(out) != 0 {
		t.Fatal("no rows means no output")
	}

	ragged := [][]float64{{1, 2, 3, 4}, {1, 2}}
	out := Deviation(ragged, 2)
	if len(out[0]) != 2 {
		t.Fatalf("output must be trimmed to the shortest row, got %d", len(out[0]))
	}
}

func TestDeviationHandlesIdenticalPeers(t *testing.T) {
	rows := [][]float64{{5, 5, 5}, {5, 5, 5}, {5, 5, 9}}
	out := Deviation(rows, 2)
	if !math.IsInf(out[2][2], 1) {
		t.Fatalf("with zero peer spread any deviation is infinite, got %v", out[2][2])
	}
	if out[0][2] != 0 {
		t.Fatalf("a series sitting on the peer median must score 0, got %v", out[0][2])
	}
}

func TestCorroborateRequiresAgreement(t *testing.T) {
	n := 500
	target := make([]float64, n)
	partner := make([]float64, n)
	for i := range target {
		target[i] = 0.5
		partner[i] = 0.5
	}
	real := 200
	lone := 400
	target[real], partner[real+2] = 1e-6, 1e-6
	target[lone] = 1e-6

	streams := Corroborate(target, [][]float64{partner}, Options{Lag: 5, Causal: false})
	if len(streams) != 2 {
		t.Fatalf("expected the target plus one corroborator, got %d", len(streams))
	}
	combined := fuse.AgreeStreams(streams, 2)
	if combined[real] >= combined[lone] {
		t.Fatalf("a corroborated event must beat a lone one: %v vs %v", combined[real], combined[lone])
	}
	if combined[lone] < 0.01 {
		t.Fatalf("a lone detector must not satisfy k=2, got %v", combined[lone])
	}
}

func TestCorroborateCausalIgnoresTheFuture(t *testing.T) {
	n := 200
	target := make([]float64, n)
	partner := make([]float64, n)
	for i := range target {
		target[i] = 0.5
		partner[i] = 0.5
	}
	target[100] = 1e-6
	partner[105] = 1e-6

	causal := Corroborate(target, [][]float64{partner}, Options{Lag: 10, Causal: true})
	symmetric := Corroborate(target, [][]float64{partner}, Options{Lag: 10, Causal: false})
	if causal[1][100] <= symmetric[1][100] {
		t.Fatalf("a causal window must not see the partner's later spike: %v vs %v",
			causal[1][100], symmetric[1][100])
	}

	partner2 := make([]float64, n)
	for i := range partner2 {
		partner2[i] = 0.5
	}
	partner2[95] = 1e-6
	back := Corroborate(target, [][]float64{partner2}, Options{Lag: 10, Causal: true})
	if back[1][100] > 0.01 {
		t.Fatalf("a causal window must see the partner's earlier spike, got %v", back[1][100])
	}
}

func TestCorroborateCorrectsForWindowScanning(t *testing.T) {
	n := 50
	target := make([]float64, n)
	partner := make([]float64, n)
	for i := range target {
		target[i] = 0.5
		partner[i] = 0.02
	}
	narrow := Corroborate(target, [][]float64{partner}, Options{Lag: 0})
	wide := Corroborate(target, [][]float64{partner}, Options{Lag: 20, Causal: true})
	if wide[1][30] <= narrow[1][30] {
		t.Fatalf("scanning a wider window must inflate the corroborating p-value: %v vs %v",
			wide[1][30], narrow[1][30])
	}
	for _, s := range wide {
		for i, v := range s {
			if v < 0 || v > 1 {
				t.Fatalf("p-values must stay in [0,1], got %v at %d", v, i)
			}
		}
	}
}

func TestPeerRaisesPrecisionOnAFleet(t *testing.T) {
	n := 3000
	m := 10
	rows := fleet(m, n, 7)
	truth := make([]bool, n)
	for _, at := range []int{800, 1500, 2200} {
		for k := 0; k < 8; k++ {
			rows[3][at+k] *= 2.2
			truth[at+k] = true
		}
	}
	for _, at := range []int{600, 1100, 1900, 2600} {
		for k := 0; k < 8; k++ {
			for s := range rows {
				rows[s][at+k] *= 2.2
			}
		}
	}

	solo := Normalize(rows, 100)[3]
	peerZ := Scores(rows, Options{Window: 100})[3]

	score := func(z []float64, thr float64) (hits, alarms int) {
		for i, v := range z {
			if i < 200 || math.Abs(v) < thr {
				continue
			}
			alarms++
			if truth[i] {
				hits++
			}
		}
		return hits, alarms
	}
	sh, sa := score(solo, 6)
	ph, pa := score(peerZ, 6)
	if sa == 0 || pa == 0 {
		t.Fatalf("both views must fire: solo=%d peer=%d", sa, pa)
	}
	soloPrec := float64(sh) / float64(sa)
	peerPrec := float64(ph) / float64(pa)
	if peerPrec <= soloPrec {
		t.Fatalf("peer comparison must raise precision by ignoring fleet-wide moves: %.3f vs %.3f",
			peerPrec, soloPrec)
	}
	if ph == 0 {
		t.Fatal("peer comparison must still catch the single-instance faults")
	}
	if soloPrec > 0.6 {
		t.Fatalf("the fixture should make the solo view noisy, got %.3f", soloPrec)
	}
}
