package model

import "math"

type Regime struct {
	Start int     `json:"start"`
	End   int     `json:"end"`
	N     int     `json:"n"`
	Mean  float64 `json:"mean"`
	Std   float64 `json:"std"`
}

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

type RegimeShiftResult struct {
	Shifted     bool    `json:"shifted"`
	Probability float64 `json:"probability"`
	At          int     `json:"at"`
	FromMean    float64 `json:"from_mean"`
	ToMean      float64 `json:"to_mean"`
}

func RegimeShift(series []float64) RegimeShiftResult {
	regs := Regimes(series)
	if len(regs) < 2 {
		return RegimeShiftResult{}
	}
	a := regs[len(regs)-2]
	b := regs[len(regs)-1]

	va, vb := a.Std*a.Std, b.Std*b.Std
	denom := math.Sqrt(va/float64(a.N) + vb/float64(b.N))
	prob := 1.0
	if denom > 0 {
		tstat := math.Abs(b.Mean-a.Mean) / denom
		prob = 1 - 2*normUpperTail(tstat)
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
