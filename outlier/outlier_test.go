package outlier

import (
	"math"
	"math/rand"
	"testing"
)

func cluster(rnd *rand.Rand, n int, centre []float64, spread float64) [][]float64 {
	rows := make([][]float64, n)
	for i := range rows {
		r := make([]float64, len(centre))
		for f := range centre {
			r[f] = centre[f] + rnd.NormFloat64()*spread
		}
		rows[i] = r
	}
	return rows
}

func TestDetectFindsPlantedOutliers(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	features := []string{"cpu", "mem", "io"}
	rows := cluster(rnd, 200, []float64{50, 60, 10}, 3)

	planted := map[int]bool{}
	for _, p := range [][]float64{
		{95, 62, 11}, {49, 5, 9}, {51, 59, 90}, {5, 5, 5}, {90, 95, 80},
	} {
		rows = append(rows, p)
		planted[len(rows)-1] = true
	}

	res, err := Detect(features, rows, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != len(rows) {
		t.Fatalf("expected one result per row, got %d", len(res))
	}

	top := Top(res, 5)
	found := 0
	for _, r := range top {
		if planted[r.Index] {
			found++
		}
	}
	if found != 5 {
		t.Fatalf("expected all 5 planted outliers in the top 5, found %d: %+v", found, top)
	}
	for _, r := range top {
		if r.Score <= 0.5 {
			t.Errorf("planted outlier %d scored only %.3f", r.Index, r.Score)
		}
	}

	var inlierScores []float64
	for _, r := range res {
		if !planted[r.Index] {
			inlierScores = append(inlierScores, r.Score)
		}
	}
	if m := median(inlierScores); m > 0.15 {
		t.Errorf("typical rows should score near zero, median was %.3f", m)
	}
}

func TestFeatureInfluencePointsAtTheGuiltyColumn(t *testing.T) {
	rnd := rand.New(rand.NewSource(11))
	features := []string{"cpu", "mem", "io_wait"}
	rows := cluster(rnd, 150, []float64{50, 60, 10}, 2)

	rows = append(rows, []float64{50, 60, 95})
	idx := len(rows) - 1

	res, err := Detect(features, rows, Options{})
	if err != nil {
		t.Fatal(err)
	}
	inf := res[idx].Influence
	if inf == nil {
		t.Fatal("no influence computed")
	}
	if inf["io_wait"] < 0.8 {
		t.Fatalf("io_wait should dominate the explanation, got %+v", inf)
	}
	var sum float64
	for _, v := range inf {
		sum += v
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Errorf("influence must sum to 1, got %v", sum)
	}
}

func TestNoFalsePositivesOnACleanPopulation(t *testing.T) {
	rnd := rand.New(rand.NewSource(3))
	rows := cluster(rnd, 300, []float64{10, 20}, 1)
	res, err := Detect([]string{"a", "b"}, rows, Options{})
	if err != nil {
		t.Fatal(err)
	}
	flagged := 0
	for _, r := range res {
		if r.Score > 0.5 {
			flagged++
		}
	}

	if flagged > len(rows)/20 {
		t.Fatalf("%d/%d rows flagged on a clean population", flagged, len(rows))
	}
}

func TestDegenerateInputs(t *testing.T) {
	same := [][]float64{{1, 1}, {1, 1}, {1, 1}, {1, 1}, {1, 1}}
	res, err := Detect([]string{"a", "b"}, same, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if math.IsNaN(r.Score) || math.IsInf(r.Score, 0) {
			t.Fatalf("identical rows produced a non-finite score: %+v", r)
		}
		if r.Score > 0.5 {
			t.Fatalf("identical rows cannot be outliers, got %.3f", r.Score)
		}
	}

	rows := [][]float64{{1, 5}, {2, 5}, {3, 5}, {50, 5}}
	if _, err := Detect([]string{"v", "const"}, rows, Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestDetectValidatesInput(t *testing.T) {
	if _, err := Detect([]string{"a"}, [][]float64{{1}, {2}}, Options{}); err == nil {
		t.Error("expected an error for fewer than 3 rows")
	}
	ragged := [][]float64{{1, 2}, {3}, {4, 5}}
	if _, err := Detect([]string{"a", "b"}, ragged, Options{}); err == nil {
		t.Error("expected an error for a ragged row")
	}
}

func TestAllMethodsAgreeOnAnObviousOutlier(t *testing.T) {
	rnd := rand.New(rand.NewSource(5))
	rows := cluster(rnd, 100, []float64{0, 0}, 1)
	rows = append(rows, []float64{40, 40})
	res, err := Detect([]string{"x", "y"}, rows, Options{K: 8})
	if err != nil {
		t.Fatal(err)
	}
	m := res[len(rows)-1].Methods
	for _, name := range []string{"knn", "kth_nn", "ldof"} {
		if m[name] < 0.9 {
			t.Errorf("method %s scored only %.3f on an obvious outlier", name, m[name])
		}
	}
}
