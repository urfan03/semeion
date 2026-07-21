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
func (GoProvider) ChangePoints(s []float64) []int                 { return changePoints(s) }
func (GoProvider) FitDistribution(samples []float64) Distribution { return fitDistribution(samples) }

// ── Seasonality: autocorrelation, first prominent peak ───────────────────────

func detectSeasonality(x []float64) []int {
	n := len(x)
	if n < 8 {
		return nil
	}
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
	// The period is the first prominent local maximum (lag ≥ 2): ACF descends
	// from lag 0, dips, then peaks again at the period. Requiring a rise into the
	// peak avoids picking the high-but-descending small lags.
	for lag := 2; lag < maxLag; lag++ {
		if acf[lag] >= 0.3 && acf[lag] > acf[lag-1] && acf[lag] >= acf[lag+1] {
			return []int{lag}
		}
	}
	return nil
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

// bestSplit returns the split offset maximising the mean-difference statistic
// over sub, and that statistic. Offset k means left=sub[:k], right=sub[k:].
func bestSplit(sub []float64) (int, float64) {
	n := len(sub)
	if n < 2*cpMinSeg {
		return -1, 0
	}
	_, sd := meanStd(sub)
	if sd == 0 {
		return -1, 0
	}
	// prefix sums for O(1) segment means
	prefix := make([]float64, n+1)
	for i, v := range sub {
		prefix[i+1] = prefix[i] + v
	}
	bestK, bestScore := -1, 0.0
	for k := cpMinSeg; k <= n-cpMinSeg; k++ {
		meanL := prefix[k] / float64(k)
		meanR := (prefix[n] - prefix[k]) / float64(n-k)
		w := math.Sqrt(float64(k) * float64(n-k) / float64(n))
		score := math.Abs(meanR-meanL) * w / sd
		if score > bestScore {
			bestK, bestScore = k, score
		}
	}
	return bestK, bestScore
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
