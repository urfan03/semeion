package mp

import (
	"math"
	"testing"
)

func TestMatrixProfileFindsDiscord(t *testing.T) {
	const n, m, at = 500, 32, 250
	ts := make([]float64, n)
	for i := range ts {
		ts[i] = math.Sin(float64(i) * 0.4)
	}
	for k := 0; k < m; k++ {
		ts[at+k] = math.Sin(float64(k) * 1.7)
	}

	prof := MatrixProfile(ts, m)
	if prof == nil {
		t.Fatal("nil profile")
	}
	best, bi := -1.0, -1
	for i, v := range prof {
		if v > best {
			best, bi = v, i
		}
	}
	if bi < at-m || bi > at+m {
		t.Fatalf("discord should be near %d, argmax at %d", at, bi)
	}

	scores := PointScores(prof, n, m)
	var inDiscord, elsewhere float64
	cnt := 0
	for i := 0; i < n; i++ {
		if i >= at && i < at+m {
			if scores[i] > inDiscord {
				inDiscord = scores[i]
			}
		} else {
			elsewhere += scores[i]
			cnt++
		}
	}
	if inDiscord <= elsewhere/float64(cnt) {
		t.Fatalf("discord points should score above the average elsewhere: %.3f vs %.3f", inDiscord, elsewhere/float64(cnt))
	}
}

func TestLeftMatrixProfileNoFuture(t *testing.T) {
	const n, m = 300, 24
	ts := make([]float64, n)
	for i := range ts {
		ts[i] = math.Sin(float64(i) * 0.5)
	}
	lmp := LeftMatrixProfile(ts, m)
	if len(lmp) != n-m+1 {
		t.Fatalf("expected %d, got %d", n-m+1, len(lmp))
	}
	for i, v := range lmp {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			t.Fatalf("left mp must be finite non-negative at %d: %v", i, v)
		}
	}
}
