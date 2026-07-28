package model

import (
	"math"
	"sort"
)

type Decomposition struct {
	Trend    []float64
	Seasonal []float64
	Resid    []float64
}

type Provider interface {
	DetectSeasonality(series []float64) []int

	Decompose(series []float64, period int) Decomposition

	Forecast(series []float64, horizon int) []float64

	ForecastBands(series []float64, horizon int) []Band

	ChangePoints(series []float64) []int

	FitDistribution(samples []float64) Distribution
}

type GoProvider struct{}

func NewGoProvider() *GoProvider { return &GoProvider{} }

func (GoProvider) DetectSeasonality(series []float64) []int       { return detectSeasonality(series) }
func (GoProvider) Decompose(s []float64, p int) Decomposition     { return decompose(s, p) }
func (GoProvider) Forecast(s []float64, h int) []float64          { return forecast(s, h) }
func (GoProvider) ForecastBands(s []float64, h int) []Band        { return forecastBands(s, h) }
func (GoProvider) ChangePoints(s []float64) []int                 { return changePoints(s) }
func (GoProvider) FitDistribution(samples []float64) Distribution { return fitDistribution(samples) }

func detectSeasonality(raw []float64) []int {
	n := len(raw)
	if n < 8 {
		return nil
	}

	x := detrend(raw)
	mean := meanOf(x)
	var den float64
	for _, v := range x {
		d := v - mean
		den += d * d
	}
	if den == 0 {
		return nil
	}
	maxLag := n / 2
	if maxLag > 1440 {
		maxLag = 1440
	}
	acf := make([]float64, maxLag+1)
	for lag := 1; lag <= maxLag; lag++ {
		var num float64
		for i := 0; i+lag < n; i++ {
			num += (x[i] - mean) * (x[i+lag] - mean)
		}
		acf[lag] = num / den
	}

	type peak struct {
		lag int
		acf float64
	}
	var peaks []peak
	for lag := 2; lag < maxLag; lag++ {
		if acf[lag] >= 0.3 && acf[lag] > acf[lag-1] && acf[lag] >= acf[lag+1] {
			peaks = append(peaks, peak{lag, acf[lag]})
		}
	}
	sort.SliceStable(peaks, func(i, j int) bool {
		if peaks[i].acf != peaks[j].acf {
			return peaks[i].acf > peaks[j].acf
		}
		return peaks[i].lag < peaks[j].lag
	})
	out := make([]int, 0, 6)
	for i, p := range peaks {
		if i >= 6 {
			break
		}
		out = append(out, p.lag)
	}
	return out
}

func decompose(x []float64, period int) Decomposition {
	if period >= 2 && len(x) >= 2*period {
		return stl(x, period, 2, 2)
	}
	return classicalDecompose(x, period)
}

func classicalDecompose(x []float64, period int) Decomposition {
	n := len(x)
	d := Decomposition{Trend: make([]float64, n), Seasonal: make([]float64, n), Resid: make([]float64, n)}
	if period < 2 || n == 0 {
		copy(d.Trend, x)
		return d
	}
	trend := movingAverage(x, period)

	phaseSum := make([]float64, period)
	phaseCnt := make([]int, period)
	for i := 0; i < n; i++ {
		p := i % period
		phaseSum[p] += x[i] - trend[i]
		phaseCnt[p]++
	}
	phase := make([]float64, period)
	for p := 0; p < period; p++ {
		if phaseCnt[p] > 0 {
			phase[p] = phaseSum[p] / float64(phaseCnt[p])
		}
	}
	pm := meanOf(phase)
	for p := range phase {
		phase[p] -= pm
	}
	for i := 0; i < n; i++ {
		d.Trend[i] = trend[i]
		d.Seasonal[i] = phase[i%period]
		d.Resid[i] = x[i] - trend[i] - d.Seasonal[i]
	}
	return d
}

func movingAverage(x []float64, w int) []float64 {
	n := len(x)
	out := make([]float64, n)
	half := w / 2
	for i := 0; i < n; i++ {
		lo, hi := i-half, i+half
		if lo < 0 {
			lo = 0
		}
		if hi >= n {
			hi = n - 1
		}
		var s float64
		for j := lo; j <= hi; j++ {
			s += x[j]
		}
		out[i] = s / float64(hi-lo+1)
	}
	return out
}

func forecast(x []float64, horizon int) []float64 {
	out := make([]float64, horizon)
	n := len(x)
	if n == 0 || horizon <= 0 {
		return out
	}
	if periods := detectSeasonality(x); len(periods) > 0 {
		p := periods[0]
		if n >= 2*p {
			if hw := holtWinters(x, p, horizon); hw != nil {
				return hw
			}
		}
		d := decompose(x, p)
		slope, intercept := linFit(d.Trend)
		for h := 0; h < horizon; h++ {
			j := n + h
			out[h] = intercept + slope*float64(j) + d.Seasonal[j%p]
		}
		return out
	}
	slope, intercept := linFit(x)
	for h := 0; h < horizon; h++ {
		out[h] = intercept + slope*float64(n+h)
	}
	return out
}

func holtWinters(x []float64, period, horizon int) []float64 {
	n := len(x)
	if period < 2 || n < 2*period || horizon <= 0 {
		return nil
	}
	best := math.Inf(1)
	var bestFit []float64
	for _, a := range []float64{0.1, 0.3, 0.5, 0.7} {
		for _, b := range []float64{0.05, 0.1, 0.3} {
			for _, g := range []float64{0.1, 0.3, 0.5} {
				fit, sse := holtWintersFit(x, period, horizon, a, b, g)
				if sse < best {
					best, bestFit = sse, fit
				}
			}
		}
	}
	return bestFit
}

func holtWintersFit(x []float64, period, horizon int, alpha, beta, gamma float64) ([]float64, float64) {
	n := len(x)
	seasonal := make([]float64, period)
	var first, second float64
	for i := 0; i < period; i++ {
		first += x[i]
		second += x[period+i]
	}
	first /= float64(period)
	second /= float64(period)
	level := first
	trend := (second - first) / float64(period)
	for i := 0; i < period; i++ {
		seasonal[i] = x[i] - first
	}
	var sse float64
	for t := period; t < n; t++ {
		s := seasonal[t%period]
		predicted := level + trend + s
		e := x[t] - predicted
		sse += e * e
		prevLevel := level
		level = alpha*(x[t]-s) + (1-alpha)*(level+trend)
		trend = beta*(level-prevLevel) + (1-beta)*trend
		seasonal[t%period] = gamma*(x[t]-level) + (1-gamma)*s
	}
	out := make([]float64, horizon)
	for h := 0; h < horizon; h++ {
		out[h] = level + float64(h+1)*trend + seasonal[(n+h)%period]
	}
	return out, sse
}

type Band struct {
	Point float64 `json:"point"`
	Lower float64 `json:"lower"`
	Upper float64 `json:"upper"`
}

func forecastBands(x []float64, horizon int) []Band {
	pts := forecast(x, horizon)
	bands := make([]Band, len(pts))
	sd := residualStd(x)
	const z = 1.96
	for h := range pts {
		w := z * sd * math.Sqrt(float64(h+1))
		bands[h] = Band{Point: pts[h], Lower: pts[h] - w, Upper: pts[h] + w}
	}
	return bands
}

type Breach struct {
	WillBreach bool `json:"will_breach"`

	Step        int     `json:"step"`
	At          float64 `json:"at"`
	Threshold   float64 `json:"threshold"`
	Side        string  `json:"side"`
	Probability float64 `json:"probability"`
}

func ForecastBreach(bands []Band, threshold float64, high bool) Breach {
	b := Breach{Threshold: threshold, Side: "low"}
	if high {
		b.Side = "high"
	}
	breachProb := func(bd Band) float64 {
		sd := (bd.Upper - bd.Lower) / (2 * 1.96)
		if sd <= 0 {
			if (high && bd.Point >= threshold) || (!high && bd.Point <= threshold) {
				return 1
			}
			return 0
		}
		if high {
			return normUpperTail((threshold - bd.Point) / sd)
		}
		return normUpperTail((bd.Point - threshold) / sd)
	}
	var peakProb float64
	peakAt := 0.0
	for h, bd := range bands {
		crosses := (high && bd.Point >= threshold) || (!high && bd.Point <= threshold)
		if crosses {
			b.WillBreach = true
			b.Step = h + 1
			b.At = bd.Point
			b.Probability = breachProb(bd)
			return b
		}
		if p := breachProb(bd); p >= peakProb {
			peakProb, peakAt = p, bd.Point
		}
	}

	b.Probability = peakProb
	b.At = peakAt
	return b
}

func normUpperTail(z float64) float64 {
	return 0.5 * math.Erfc(z/math.Sqrt2)
}

func residualStd(x []float64) float64 {
	if len(x) < 3 {
		return 0
	}
	var resid []float64
	if periods := detectSeasonality(x); len(periods) > 0 {
		resid = decompose(x, periods[0]).Resid
	} else {
		slope, intercept := linFit(x)
		resid = make([]float64, len(x))
		for i, v := range x {
			resid[i] = v - (intercept + slope*float64(i))
		}
	}
	_, sd := meanStd(resid)
	return sd
}

func linFit(y []float64) (slope, intercept float64) {
	n := len(y)
	if n < 2 {
		if n == 1 {
			return 0, y[0]
		}
		return 0, 0
	}
	var sx, sy, sxy, sxx float64
	for i, v := range y {
		x := float64(i)
		sx += x
		sy += v
		sxy += x * v
		sxx += x * x
	}
	fn := float64(n)
	den := fn*sxx - sx*sx
	if den == 0 {
		return 0, sy / fn
	}
	slope = (fn*sxy - sx*sy) / den
	intercept = (sy - slope*sx) / fn
	return slope, intercept
}

const (
	cpMinSeg    = 5
	cpThreshold = 5.0
)

func changePoints(x []float64) []int {
	var out []int
	var seg func(lo, hi int)
	seg = func(lo, hi int) {
		if hi-lo < 2*cpMinSeg {
			return
		}
		k, score := bestSplit(x[lo:hi])
		if k < 0 || score < cpThreshold {
			return
		}
		idx := lo + k
		out = append(out, idx)
		seg(lo, idx)
		seg(idx, hi)
	}
	seg(0, len(x))
	sort.Ints(out)
	return out
}

func bestSplit(sub []float64) (int, float64) {
	n := len(sub)
	if n < 2*cpMinSeg {
		return -1, 0
	}

	sseLine := sseLinear(sub)
	if sseLine <= 0 {
		return -1, 0
	}
	prefix := make([]float64, n+1)
	sqPrefix := make([]float64, n+1)
	for i, v := range sub {
		prefix[i+1] = prefix[i] + v
		sqPrefix[i+1] = sqPrefix[i] + v*v
	}

	sseSplit := func(k int) float64 {
		sumL, sumR := prefix[k], prefix[n]-prefix[k]
		sqL, sqR := sqPrefix[k], sqPrefix[n]-sqPrefix[k]
		return (sqL - sumL*sumL/float64(k)) + (sqR - sumR*sumR/float64(n-k))
	}
	bestK, bestSSE := -1, math.Inf(1)
	for k := cpMinSeg; k <= n-cpMinSeg; k++ {
		if s := sseSplit(k); s < bestSSE {
			bestK, bestSSE = k, s
		}
	}
	if bestK < 0 {
		return -1, 0
	}

	score := (sseLine - bestSSE) / (bestSSE/float64(n-2) + 1e-9)
	return bestK, score
}

func detrend(x []float64) []float64 {
	if len(x) < 3 {
		return append([]float64(nil), x...)
	}
	slope, intercept := linFit(x)
	res := make([]float64, len(x))
	for i, v := range x {
		res[i] = v - (intercept + slope*float64(i))
	}
	return res
}

func sseLinear(y []float64) float64 {
	slope, intercept := linFit(y)
	var sse float64
	for i, v := range y {
		r := v - (intercept + slope*float64(i))
		sse += r * r
	}
	return sse
}

func meanOf(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	var s float64
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}

func meanStd(x []float64) (float64, float64) {
	n := len(x)
	if n == 0 {
		return 0, 0
	}
	m := meanOf(x)
	var v float64
	for _, e := range x {
		d := e - m
		v += d * d
	}
	return m, math.Sqrt(v / float64(n))
}
