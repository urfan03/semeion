package stats

import (
	"math"
	"sort"
)

func Median(xs []float64) float64 {
	n := len(xs)
	if n == 0 {
		return 0
	}
	cp := append([]float64(nil), xs...)
	sort.Float64s(cp)
	if n%2 == 1 {
		return cp[n/2]
	}
	return (cp[n/2-1] + cp[n/2]) / 2
}

func Quantile(xs []float64, q float64) float64 {
	n := len(xs)
	if n == 0 {
		return 0
	}
	if q <= 0 {
		return minOf(xs)
	}
	if q >= 1 {
		return maxOf(xs)
	}
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	pos := q * float64(n-1)
	lo := int(pos)
	frac := pos - float64(lo)
	if lo+1 >= n {
		return c[n-1]
	}
	return c[lo] + frac*(c[lo+1]-c[lo])
}

func minOf(xs []float64) float64 {
	m := xs[0]
	for _, v := range xs[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func maxOf(xs []float64) float64 {
	m := xs[0]
	for _, v := range xs[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

func MAD(xs []float64) (med, mad float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	med = Median(xs)
	dev := make([]float64, len(xs))
	for i, x := range xs {
		dev[i] = math.Abs(x - med)
	}
	return med, Median(dev)
}

func MeanStd(xs []float64) (mean, std float64) {
	n := len(xs)
	if n == 0 {
		return 0, 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	mean = s / float64(n)
	var v float64
	for _, x := range xs {
		d := x - mean
		v += d * d
	}
	return mean, math.Sqrt(v / float64(n))
}

func ModifiedZScore(x, med, mad float64) float64 {
	if mad == 0 {
		return 0
	}
	return 0.6745 * (x - med) / mad
}

func UpperTail(z float64) float64 {
	return 0.5 * math.Erfc(z/math.Sqrt2)
}
