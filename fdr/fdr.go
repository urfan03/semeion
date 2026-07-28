package fdr

import (
	"math"
	"sort"
)

func clean(pvals []float64) ([]float64, []int) {
	vals := make([]float64, 0, len(pvals))
	idx := make([]int, 0, len(pvals))
	for i, p := range pvals {
		if math.IsNaN(p) {
			continue
		}
		if p < 0 {
			p = 0
		}
		if p > 1 {
			p = 1
		}
		vals = append(vals, p)
		idx = append(idx, i)
	}
	return vals, idx
}

func order(vals []float64) []int {
	ord := make([]int, len(vals))
	for i := range ord {
		ord[i] = i
	}
	sort.SliceStable(ord, func(a, b int) bool { return vals[ord[a]] < vals[ord[b]] })
	return ord
}

func rejectUpTo(vals []float64, idx, ord []int, cut int, n int) (float64, []bool) {
	out := make([]bool, n)
	if cut < 0 {
		return 0, out
	}
	threshold := vals[ord[cut]]
	for k := 0; k <= cut; k++ {
		out[idx[ord[k]]] = true
	}
	return threshold, out
}

func step(vals []float64, q, scale float64) int {
	m := float64(len(vals))
	ord := order(vals)
	cut := -1
	for k := range ord {
		if vals[ord[k]] <= float64(k+1)/m*q/scale {
			cut = k
		}
	}
	return cut
}

func BH(pvals []float64, q float64) (float64, []bool) {
	vals, idx := clean(pvals)
	if len(vals) == 0 || q <= 0 {
		return 0, make([]bool, len(pvals))
	}
	ord := order(vals)
	return rejectUpTo(vals, idx, ord, step(vals, q, 1), len(pvals))
}

func harmonic(m int) float64 {
	var s float64
	for i := 1; i <= m; i++ {
		s += 1 / float64(i)
	}
	return s
}

func BY(pvals []float64, q float64) (float64, []bool) {
	vals, idx := clean(pvals)
	if len(vals) == 0 || q <= 0 {
		return 0, make([]bool, len(pvals))
	}
	ord := order(vals)
	return rejectUpTo(vals, idx, ord, step(vals, q, harmonic(len(vals))), len(pvals))
}

func Storey(pvals []float64, lambda float64) float64 {
	vals, _ := clean(pvals)
	if len(vals) == 0 {
		return 1
	}
	if lambda <= 0 || lambda >= 1 {
		lambda = 0.5
	}
	above := 0
	for _, p := range vals {
		if p > lambda {
			above++
		}
	}
	pi0 := float64(above) / (float64(len(vals)) * (1 - lambda))
	if pi0 > 1 {
		return 1
	}
	if pi0 <= 0 {
		return 1 / float64(len(vals))
	}
	return pi0
}

func StoreyBH(pvals []float64, q, lambda float64) (float64, []bool) {
	vals, idx := clean(pvals)
	if len(vals) == 0 || q <= 0 {
		return 0, make([]bool, len(pvals))
	}
	pi0 := Storey(pvals, lambda)
	if pi0 <= 0 {
		pi0 = 1 / float64(len(vals))
	}
	ord := order(vals)
	return rejectUpTo(vals, idx, ord, step(vals, q, pi0), len(pvals))
}

const zeta16 = 2.2856831102859083

func Gamma(j int) float64 {
	if j < 1 {
		return 0
	}
	return 1 / (zeta16 * math.Pow(float64(j), 1.6))
}

type LORD struct {
	q       float64
	w0      float64
	t       int
	rejects []int
}

func NewLORD(q float64) *LORD {
	if q <= 0 || q >= 1 {
		q = 0.05
	}
	return &LORD{q: q, w0: q / 2}
}

func (l *LORD) Level() float64 {
	t := l.t + 1
	alpha := Gamma(t) * l.w0
	for j, tau := range l.rejects {
		gap := t - tau
		if gap < 1 {
			continue
		}
		if j == 0 {
			alpha += (l.q - l.w0) * Gamma(gap)
			continue
		}
		alpha += l.q * Gamma(gap)
	}
	if alpha > l.q {
		alpha = l.q
	}
	if alpha < 0 {
		alpha = 0
	}
	return alpha
}

func (l *LORD) Step(p float64) bool {
	alpha := l.Level()
	l.t++
	if math.IsNaN(p) {
		return false
	}
	if p <= alpha {
		l.rejects = append(l.rejects, l.t)
		return true
	}
	return false
}

func (l *LORD) Rejections() int { return len(l.rejects) }

func (l *LORD) Seen() int { return l.t }

func Online(pvals []float64, q float64) []bool {
	l := NewLORD(q)
	out := make([]bool, len(pvals))
	for i, p := range pvals {
		out[i] = l.Step(p)
	}
	return out
}

func OnlineFrom(pvals []float64, q float64, warmup int) []bool {
	if warmup < 0 {
		warmup = 0
	}
	out := make([]bool, len(pvals))
	if warmup >= len(pvals) {
		return out
	}
	tail := Online(pvals[warmup:], q)
	copy(out[warmup:], tail)
	return out
}
