// Package stats provides the classical, AI-free statistical primitives the
// engine's baseline detectors are built on. They are chosen to be robust to
// outliers in the training data, because baselines are learned from production
// traffic that naturally contains the very spikes we later want to flag.
//
// References: Iglewicz & Hoaglin (1993) for the modified z-score; Tukey (1977)
// for the MAD scale estimator.
package stats

import (
	"math"
	"sort"
)

// Median returns the median of xs without mutating it.
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

// MAD returns the median and the Median Absolute Deviation of xs. MAD has a 50%
// breakdown point — half the samples can be outliers and it still converges to
// the true scale — which is why it beats stddev for learning baselines.
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

// MeanStd returns the (population) mean and standard deviation of xs. Used only
// as a fallback when MAD is zero (a perfectly flat baseline).
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

// ModifiedZScore is the Iglewicz & Hoaglin robust z-score of x against a
// baseline described by (median, MAD). The 0.6745 constant (= Φ⁻¹(0.75)) makes
// the score comparable to a standard z for normally distributed data. Returns 0
// when MAD is zero — callers fall back to MeanStd for a flat baseline.
func ModifiedZScore(x, med, mad float64) float64 {
	if mad == 0 {
		return 0
	}
	return 0.6745 * (x - med) / mad
}

// UpperTail returns P(Z ≥ z) for a standard normal Z, via the complementary
// error function (exact, no table lookup).
func UpperTail(z float64) float64 {
	return 0.5 * math.Erfc(z/math.Sqrt2)
}
