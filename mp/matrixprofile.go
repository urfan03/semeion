package mp

import "math"

func meanStd(t []float64, m int) (mu, sig []float64) {
	n := len(t)
	l := n - m + 1
	mu = make([]float64, l)
	sig = make([]float64, l)
	var sum, sumSq float64
	for i := 0; i < m; i++ {
		sum += t[i]
		sumSq += t[i] * t[i]
	}
	fm := float64(m)
	for i := 0; i < l; i++ {
		if i > 0 {
			sum += t[i+m-1] - t[i-1]
			sumSq += t[i+m-1]*t[i+m-1] - t[i-1]*t[i-1]
		}
		mean := sum / fm
		mu[i] = mean
		v := sumSq/fm - mean*mean
		if v < 0 {
			v = 0
		}
		sig[i] = math.Sqrt(v)
	}
	return mu, sig
}

func dist(qt, mi, mj, si, sj, fm float64) float64 {
	if si <= 0 || sj <= 0 {
		return math.Sqrt(2 * fm)
	}
	corr := (qt - fm*mi*mj) / (fm * si * sj)
	if corr > 1 {
		corr = 1
	} else if corr < -1 {
		corr = -1
	}
	d := 2 * fm * (1 - corr)
	if d < 0 {
		d = 0
	}
	return math.Sqrt(d)
}

func exclusion(m int) int {
	e := m / 4
	if e < 1 {
		e = 1
	}
	return e
}

// MatrixProfile computes the z-normalized self-join matrix profile of t with
// window m via STOMP. mp[i] is the distance from subsequence i to its nearest
// non-trivial neighbour; large values are discords (anomalies).
func MatrixProfile(t []float64, m int) []float64 {
	return stomp(t, m, false)
}

func stomp(t []float64, m int, constMatch bool) []float64 {
	n := len(t)
	if m < 2 || n < 2*m {
		return nil
	}
	l := n - m + 1
	mu, sig := meanStd(t, m)
	fm := float64(m)
	excl := exclusion(m)
	eps := flatEpsilon(t)

	first := make([]float64, l)
	for j := 0; j < l; j++ {
		var s float64
		for k := 0; k < m; k++ {
			s += t[k] * t[j+k]
		}
		first[j] = s
	}
	qt := make([]float64, l)
	copy(qt, first)

	mp := make([]float64, l)
	for i := range mp {
		mp[i] = math.Inf(1)
	}
	for i := 0; i < l; i++ {
		if i > 0 {
			for j := l - 1; j >= 1; j-- {
				qt[j] = qt[j-1] - t[i-1]*t[j-1] + t[i+m-1]*t[j+m-1]
			}
			qt[0] = first[i]
		}
		for j := 0; j < l; j++ {
			if abs(i-j) < excl {
				continue
			}
			var d float64
			switch {
			case constMatch && sig[i] <= eps && sig[j] <= eps:
				d = 0
			case constMatch && (sig[i] <= eps || sig[j] <= eps):
				d = math.Sqrt(2 * fm)
			default:
				d = dist(qt[j], mu[i], mu[j], sig[i], sig[j], fm)
			}
			if d < mp[i] {
				mp[i] = d
			}
		}
		if math.IsInf(mp[i], 1) {
			mp[i] = 0
		}
	}
	return mp
}

// LeftMatrixProfile is the online variant: lmp[i] is the distance from
// subsequence i to its nearest neighbour among earlier subsequences only,
// so each point is scored using only the past (no future leakage).
func LeftMatrixProfile(t []float64, m int) []float64 {
	n := len(t)
	if m < 2 || n < 2*m {
		return nil
	}
	l := n - m + 1
	mu, sig := meanStd(t, m)
	fm := float64(m)
	excl := exclusion(m)
	lmp := make([]float64, l)
	for i := 0; i < l; i++ {
		best := math.Inf(1)
		for j := 0; j <= i-excl; j++ {
			var s float64
			for k := 0; k < m; k++ {
				s += t[i+k] * t[j+k]
			}
			d := dist(s, mu[i], mu[j], sig[i], sig[j], fm)
			if d < best {
				best = d
			}
		}
		if math.IsInf(best, 1) {
			best = 0
		}
		lmp[i] = best
	}
	return lmp
}

// PointScores maps a per-subsequence matrix profile to a per-timestamp score of
// length n: each point takes the max profile value over the subsequences
// covering it, so a discord raises every point inside it.
func PointScores(profile []float64, n, m int) []float64 {
	out := make([]float64, n)
	for i, v := range profile {
		for k := 0; k < m; k++ {
			if i+k < n && v > out[i+k] {
				out[i+k] = v
			}
		}
	}
	return out
}

func flatEpsilon(t []float64) float64 {
	var scale float64
	for _, v := range t {
		if a := math.Abs(v); a > scale {
			scale = a
		}
	}
	if scale < 1 {
		scale = 1
	}
	return 1e-7 * scale
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
