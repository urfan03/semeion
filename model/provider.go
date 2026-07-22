// Package model provides the heavy, non-streaming model math that the streaming
// engine calls periodically (not per point): seasonality discovery, seasonal
// decomposition, forecasting and change-point detection.
//
// Provider is an interface so the implementation can be swapped. The default is
// GoProvider — pure Go, zero dependencies, always in the single binary. An
// optional Python sidecar (statsmodels / scipy / ruptures) can implement the
// same interface over gRPC for research-grade models; see the `python/` dir.
package model

import (
	"math"
	"sort"
)

// Decomposition splits a series into additive components: series ≈ trend +
// seasonal + resid.
type Decomposition struct {
	Trend    []float64
	Seasonal []float64
	Resid    []float64
}

// Provider computes the heavy models. All methods are pure (no state, no I/O),
// so they are trivially swappable and testable.
type Provider interface {
	// DetectSeasonality returns the dominant period(s) in samples (nil if none).
	DetectSeasonality(series []float64) []int
	// Decompose splits the series at the given period (additive).
	Decompose(series []float64, period int) Decomposition
	// Forecast projects the series horizon steps ahead.
	Forecast(series []float64, horizon int) []float64
	// ForecastBands is Forecast with a prediction interval per step.
	ForecastBands(series []float64, horizon int) []Band
	// ChangePoints returns indices where the series' mean level shifts.
	ChangePoints(series []float64) []int
	// FitDistribution selects the best-fit distribution for the samples.
	FitDistribution(samples []float64) Distribution
}

// GoProvider is the pure-Go, dependency-free implementation.
type GoProvider struct{}

// NewGoProvider returns the default provider.
func NewGoProvider() *GoProvider { return &GoProvider{} }

func (GoProvider) DetectSeasonality(series []float64) []int       { return detectSeasonality(series) }
func (GoProvider) Decompose(s []float64, p int) Decomposition     { return decompose(s, p) }
func (GoProvider) Forecast(s []float64, h int) []float64          { return forecast(s, h) }
func (GoProvider) ForecastBands(s []float64, h int) []Band        { return forecastBands(s, h) }
func (GoProvider) ChangePoints(s []float64) []int                 { return changePoints(s) }
func (GoProvider) FitDistribution(samples []float64) Distribution { return fitDistribution(samples) }

// ── Seasonality: autocorrelation, first prominent peak ───────────────────────

func detectSeasonality(raw []float64) []int {
	n := len(raw)
	if n < 8 {
		return nil
	}
	// Detrend first: the autocorrelation of a trending series stays high and
	// slowly descending, which masks the seasonal peak (a sine + strong trend
	// would otherwise report NO period). Removing the linear fit exposes the
	// cyclic component. A pure step/level is left mostly intact, which is fine —
	// seasonality is judged on the oscillation around the trend.
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
		maxLag = 1440 // cap (a day of minutes) — enough for common periods
	}
	acf := make([]float64, maxLag+1)
	for lag := 1; lag <= maxLag; lag++ {
		var num float64
		for i := 0; i+lag < n; i++ {
			num += (x[i] - mean) * (x[i+lag] - mean)
		}
		acf[lag] = num / den
	}
	// Collect the prominent local maxima (lag ≥ 2), ascending. The ACF of a
	// signal with nested cycles (e.g. daily AND weekly) peaks at each period and
	// its harmonics; returning them all lets the caller pick — a longer period
	// whose phases subsume the shorter cycle captures BOTH seasonalities.
	var peaks []int
	for lag := 2; lag < maxLag; lag++ {
		if acf[lag] >= 0.3 && acf[lag] > acf[lag-1] && acf[lag] >= acf[lag+1] {
			peaks = append(peaks, lag)
			if len(peaks) >= 6 {
				break
			}
		}
	}
	return peaks
}

// ── Additive decomposition ───────────────────────────────────────────────────

func decompose(x []float64, period int) Decomposition {
	n := len(x)
	d := Decomposition{Trend: make([]float64, n), Seasonal: make([]float64, n), Resid: make([]float64, n)}
	if period < 2 || n == 0 {
		copy(d.Trend, x)
		return d
	}
	trend := movingAverage(x, period)
	// seasonal = mean of (x - trend) per phase, centred to zero mean.
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

// movingAverage is a centred moving average of window w; edge windows shrink.
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

// ── Forecast: seasonal-naïve + linear trend ──────────────────────────────────

func forecast(x []float64, horizon int) []float64 {
	out := make([]float64, horizon)
	n := len(x)
	if n == 0 || horizon <= 0 {
		return out
	}
	if periods := detectSeasonality(x); len(periods) > 0 {
		p := periods[0]
		d := decompose(x, p)
		// Anchor on the fitted trend LINE (robust to edge bias of the last MA
		// point) + the seasonal component for the future phase.
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

// Band is a forecast point with a prediction interval.
type Band struct {
	Point float64 `json:"point"`
	Lower float64 `json:"lower"`
	Upper float64 `json:"upper"`
}

// forecastBands wraps the point forecast with a 95% prediction interval derived
// from the in-sample residual spread, widened with the horizon (uncertainty
// grows the further out you project). This is the headline Elastic ML forecast
// output — a point value alone is not actionable.
func forecastBands(x []float64, horizon int) []Band {
	pts := forecast(x, horizon)
	bands := make([]Band, len(pts))
	sd := residualStd(x)
	const z = 1.96 // ~95%
	for h := range pts {
		// Widen ∝ √(1 + h/n): the one-step interval grows toward a random-walk
		// band as the horizon extends.
		w := z * sd * math.Sqrt(1+float64(h)/math.Max(1, float64(len(x))))
		bands[h] = Band{Point: pts[h], Lower: pts[h] - w, Upper: pts[h] + w}
	}
	return bands
}

// residualStd is the std of the series' residuals after removing trend (and
// seasonality when present) — the noise the forecast can't explain.
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

// linFit is the least-squares line (slope, intercept) of y over its index.
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

// ── Change points: mean-shift binary segmentation ───────────────────────────
//
// For each segment we find the split that best separates two means (a t-like
// statistic); if it clears the threshold we record it and recurse on both
// halves. This localises a step change at its true position, unlike a global
// CUSUM which also fires inside a stable regime.

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

// bestSplit finds the split that best models sub as two constant levels, and
// scores it by how much better that two-level model fits than a single LINEAR
// trend. Scoring against a trend (not against a flat mean) is what stops a
// smooth ramp from being reported as a staircase of steps: a ramp is explained
// by the line (so the two-level model wins nothing), while a genuine level shift
// is not (a line can't fit a step). Offset k means left=sub[:k], right=sub[k:].
func bestSplit(sub []float64) (int, float64) {
	n := len(sub)
	if n < 2*cpMinSeg {
		return -1, 0
	}
	// Residual SSE of the single-line (trend) model — the null hypothesis.
	sseLine := sseLinear(sub)
	if sseLine <= 0 {
		return -1, 0 // a perfect line: nothing to explain with a step
	}
	prefix := make([]float64, n+1)
	sqPrefix := make([]float64, n+1)
	for i, v := range sub {
		prefix[i+1] = prefix[i] + v
		sqPrefix[i+1] = sqPrefix[i] + v*v
	}
	// SSE of a two-constant model split at k, via prefix sums.
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
	// F-like statistic: how much the two-level model reduces residual error
	// relative to the trend model. Large only for a real step.
	score := (sseLine - bestSSE) / (bestSSE/float64(n-2) + 1e-9)
	return bestK, score
}

// detrend subtracts the global least-squares linear fit from x.
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

// sseLinear returns the residual sum of squares of the least-squares line fit.
func sseLinear(y []float64) float64 {
	slope, intercept := linFit(y)
	var sse float64
	for i, v := range y {
		r := v - (intercept + slope*float64(i))
		sse += r * r
	}
	return sse
}

// ── helpers ──────────────────────────────────────────────────────────────────

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
