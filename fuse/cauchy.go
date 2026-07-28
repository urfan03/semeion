package fuse

import "math"

func Cauchy(pvals, weights []float64) float64 {
	var stat, total float64
	k := 0
	for i, p := range pvals {
		if math.IsNaN(p) {
			continue
		}
		if p <= 0 {
			p = 1e-300
		}
		if p > 1 {
			p = 1
		}
		w := 1.0
		if i < len(weights) {
			w = weights[i]
		}
		if w <= 0 {
			continue
		}
		total += w
		k++
		if p < 1e-15 {
			stat += w / (p * math.Pi)
			continue
		}
		stat += w * math.Tan((0.5-p)*math.Pi)
	}
	if k == 0 || total <= 0 {
		return 1
	}
	stat /= total
	p := 0.5 - math.Atan(stat)/math.Pi
	if math.IsNaN(p) {
		return 1
	}
	if p <= 0 {
		return 1e-300
	}
	if p > 1 {
		return 1
	}
	return p
}

func CauchyStreams(pstreams [][]float64, weights []float64) []float64 {
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
		out[i] = Cauchy(buf, weights)
	}
	return out
}

func HarmonicMean(pvals []float64) float64 {
	var sum float64
	k := 0
	for _, p := range pvals {
		if math.IsNaN(p) {
			continue
		}
		if p <= 0 {
			return 1e-300
		}
		if p > 1 {
			p = 1
		}
		sum += 1 / p
		k++
	}
	if k == 0 || sum <= 0 {
		return 1
	}
	return clampP(float64(k) / sum)
}
