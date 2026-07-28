package fuse

import (
	"math"
	"sort"
)

func betaCDF(x, a, b float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	lab, _ := math.Lgamma(a + b)
	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	front := math.Exp(lab - la - lb + a*math.Log(x) + b*math.Log(1-x))
	if x < (a+1)/(a+b+2) {
		return front * betaContinued(x, a, b) / a
	}
	return 1 - front*betaContinued(1-x, b, a)/b
}

func betaContinued(x, a, b float64) float64 {
	const tiny = 1e-300
	qab, qap, qam := a+b, a+1, a-1
	c := 1.0
	d := 1 - qab*x/qap
	if math.Abs(d) < tiny {
		d = tiny
	}
	d = 1 / d
	h := d
	for m := 1; m <= 300; m++ {
		fm := float64(m)
		m2 := 2 * fm

		aa := fm * (b - fm) * x / ((qam + m2) * (a + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		h *= d * c

		aa = -(a + fm) * (qab + fm) * x / ((a + m2) * (qap + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < 1e-14 {
			break
		}
	}
	return h
}

func cleanP(pvals []float64) []float64 {
	out := make([]float64, 0, len(pvals))
	for _, p := range pvals {
		if math.IsNaN(p) {
			continue
		}
		if p <= 0 {
			p = 1e-300
		}
		if p > 1 {
			p = 1
		}
		out = append(out, p)
	}
	return out
}

func Agree(pvals []float64, k int) float64 {
	ps := cleanP(pvals)
	m := len(ps)
	if m == 0 {
		return 1
	}
	if k < 1 {
		k = 1
	}
	if k > m {
		return 1
	}
	sort.Float64s(ps)
	return clampP(betaCDF(ps[k-1], float64(k), float64(m-k+1)))
}

func Majority(pvals []float64) float64 {
	m := len(cleanP(pvals))
	if m == 0 {
		return 1
	}
	return Agree(pvals, (m+2)/2)
}

func AgreeStreams(pstreams [][]float64, k int) []float64 {
	if len(pstreams) == 0 {
		return nil
	}
	n := len(pstreams[0])
	for _, s := range pstreams {
		if len(s) < n {
			n = len(s)
		}
	}
	if n == 0 {
		return nil
	}
	out := make([]float64, n)
	buf := make([]float64, len(pstreams))
	for i := 0; i < n; i++ {
		for j, s := range pstreams {
			buf[j] = s[i]
		}
		out[i] = Agree(buf, k)
	}
	return out
}

func Vote(pvals []float64, tau float64, k int) bool {
	fired := 0
	for _, p := range cleanP(pvals) {
		if p < tau {
			fired++
		}
	}
	return fired >= k
}
