package model

import "math"

// Regime detection turns the raw change-point indices into something actionable:
// the stable SEGMENTS between shifts, each summarized by its level and spread,
// and a probabilistic measure of whether the series has entered a NEW regime (a
// sustained level change, not a transient spike). This is what lets a caller say
// "the baseline moved" rather than "there was a spike at t" — a regime shift is a
// different event from a point anomaly and warrants a different response
// (rebaseline / investigate a deploy, not page on every bucket at the new level).

// Regime is a stable segment [Start, End) with its level and spread.
type Regime struct {
	Start int     `json:"start"`
	End   int     `json:"end"`
	N     int     `json:"n"`
	Mean  float64 `json:"mean"`
	Std   float64 `json:"std"`
}

// Regimes segments a series at its detected change points and summarizes each
// segment. A series with no change point is a single regime.
func Regimes(series []float64) []Regime {
	n := len(series)
	if n == 0 {
		return nil
	}
	cps := changePoints(series)
	bounds := append([]int{0}, cps...)
	bounds = append(bounds, n)
	out := make([]Regime, 0, len(bounds)-1)
	for i := 0; i+1 < len(bounds); i++ {
		lo, hi := bounds[i], bounds[i+1]
		if hi <= lo {
			continue
		}
		mean, std := meanStd(series[lo:hi])
		out = append(out, Regime{Start: lo, End: hi, N: hi - lo, Mean: mean, Std: std})
	}
	return out
}

// RegimeShiftResult describes whether the series' most recent regime is a genuine
// shift from the previous one.
type RegimeShiftResult struct {
	Shifted     bool    `json:"shifted"`
	Probability float64 `json:"probability"` // P(the level genuinely changed) in [0,1]
	At          int     `json:"at"`          // index where the latest regime starts (0 if none)
	FromMean    float64 `json:"from_mean"`
	ToMean      float64 `json:"to_mean"`
}

// RegimeShift compares the two most recent regimes with a Welch two-sample test
// and maps the statistic to the probability that their means genuinely differ
// (1 − two-sided p-value under a normal approximation). A high probability means
// the baseline has moved — a regime change, not a transient. Fewer than two
// regimes → no shift.
func RegimeShift(series []float64) RegimeShiftResult {
	regs := Regimes(series)
	if len(regs) < 2 {
		return RegimeShiftResult{}
	}
	a := regs[len(regs)-2]
	b := regs[len(regs)-1]
	// Welch's t: (mB − mA) / sqrt(sA²/nA + sB²/nB).
	va, vb := a.Std*a.Std, b.Std*b.Std
	denom := math.Sqrt(va/float64(a.N) + vb/float64(b.N))
	prob := 1.0
	if denom > 0 {
		tstat := math.Abs(b.Mean-a.Mean) / denom
		prob = 1 - 2*normUpperTail(tstat) // 1 − two-sided p-value
	} else if a.Mean == b.Mean {
		prob = 0
	}
	if prob < 0 {
		prob = 0
	}
	if prob > 1 {
		prob = 1
	}
	return RegimeShiftResult{
		Shifted:     prob >= 0.9,
		Probability: prob,
		At:          b.Start,
		FromMean:    a.Mean,
		ToMean:      b.Mean,
	}
}
