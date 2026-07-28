package model

import (
	"math"
	"sort"
)

func tricube(d float64) float64 {
	if d >= 1 {
		return 0
	}
	t := 1 - d*d*d
	return t * t * t
}

func nextOdd(n int) int {
	if n%2 == 0 {
		return n + 1
	}
	return n
}

func loessSmooth(y []float64, span int, robust []float64) []float64 {
	n := len(y)
	out := make([]float64, n)
	if n == 0 {
		return out
	}
	if span < 2 {
		span = 2
	}
	if span > n {
		span = n
	}
	for i := 0; i < n; i++ {
		lo := i - span/2
		if lo < 0 {
			lo = 0
		}
		hi := lo + span - 1
		if hi >= n {
			hi = n - 1
			lo = hi - span + 1
			if lo < 0 {
				lo = 0
			}
		}
		maxd := float64(i - lo)
		if float64(hi-i) > maxd {
			maxd = float64(hi - i)
		}
		if maxd == 0 {
			maxd = 1
		}
		var sw, swx, swy, swxx, swxy float64
		for j := lo; j <= hi; j++ {
			d := math.Abs(float64(j-i)) / maxd
			w := tricube(d)
			if robust != nil {
				w *= robust[j]
			}
			if w == 0 {
				continue
			}
			xj := float64(j)
			sw += w
			swx += w * xj
			swy += w * y[j]
			swxx += w * xj * xj
			swxy += w * xj * y[j]
		}
		if sw == 0 {
			out[i] = y[i]
			continue
		}
		denom := sw*swxx - swx*swx
		if denom == 0 {
			out[i] = swy / sw
			continue
		}
		b := (sw*swxy - swx*swy) / denom
		a := (swy - b*swx) / sw
		out[i] = a + b*float64(i)
	}
	return out
}

func medAbs(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	a := make([]float64, len(x))
	for i, v := range x {
		a[i] = math.Abs(v)
	}
	sort.Float64s(a)
	m := len(a) / 2
	if len(a)%2 == 1 {
		return a[m]
	}
	return (a[m-1] + a[m]) / 2
}

func stl(x []float64, period, inner, outer int) Decomposition {
	n := len(x)
	if period < 2 || n < 2*period {
		return classicalDecompose(x, period)
	}
	if inner < 1 {
		inner = 2
	}
	if outer < 1 {
		outer = 1
	}
	seasonalSpan := 7
	trendSpan := nextOdd(int(math.Ceil(1.5 * float64(period) / (1 - 1.5/float64(seasonalSpan)))))
	if trendSpan < 3 {
		trendSpan = 3
	}
	lowPassSpan := nextOdd(period)

	trend := make([]float64, n)
	seasonal := make([]float64, n)
	robust := make([]float64, n)
	for i := range robust {
		robust[i] = 1
	}

	for o := 0; o < outer; o++ {
		for k := 0; k < inner; k++ {
			detr := make([]float64, n)
			for i := range x {
				detr[i] = x[i] - trend[i]
			}
			cyc := make([]float64, n)
			for p := 0; p < period; p++ {
				var sub, subRob []float64
				var idx []int
				for i := p; i < n; i += period {
					sub = append(sub, detr[i])
					subRob = append(subRob, robust[i])
					idx = append(idx, i)
				}
				sm := loessSmooth(sub, seasonalSpan, subRob)
				for j, i := range idx {
					cyc[i] = sm[j]
				}
			}
			lp := movingAverage(cyc, period)
			lp = movingAverage(lp, period)
			lp = movingAverage(lp, 3)
			lp = loessSmooth(lp, lowPassSpan, nil)
			for i := range seasonal {
				seasonal[i] = cyc[i] - lp[i]
			}
			deseason := make([]float64, n)
			for i := range x {
				deseason[i] = x[i] - seasonal[i]
			}
			trend = loessSmooth(deseason, trendSpan, robust)
		}
		resid := make([]float64, n)
		for i := range x {
			resid[i] = x[i] - trend[i] - seasonal[i]
		}
		h := 6 * medAbs(resid)
		if h == 0 {
			break
		}
		for i := range resid {
			u := math.Abs(resid[i]) / h
			if u >= 1 {
				robust[i] = 0
			} else {
				t := 1 - u*u
				robust[i] = t * t
			}
		}
	}

	d := Decomposition{Trend: trend, Seasonal: seasonal, Resid: make([]float64, n)}
	for i := range x {
		d.Resid[i] = x[i] - trend[i] - seasonal[i]
	}
	return d
}
