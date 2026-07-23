package model

import "math"

type Distribution struct {
	Family string    `json:"family"`
	Params []float64 `json:"params"`
	LogLik float64   `json:"loglik"`
}

func (d Distribution) Tail(x float64) float64 {
	var p float64
	switch d.Family {
	case "normal":
		mu, sd := d.Params[0], d.Params[1]
		if sd <= 0 {
			return 1
		}
		lower := 0.5 * math.Erfc(-(x-mu)/(sd*math.Sqrt2))
		p = 2 * math.Min(lower, 1-lower)
	case "lognormal":
		mu, sd := d.Params[0], d.Params[1]
		if x <= 0 || sd <= 0 {
			return 1
		}
		lower := 0.5 * math.Erfc(-(math.Log(x)-mu)/(sd*math.Sqrt2))
		p = 2 * math.Min(lower, 1-lower)
	case "exponential":

		rate := d.Params[0]
		if rate <= 0 {
			return 1
		}
		if x < 0 {
			return 1
		}
		upper := math.Exp(-rate * x)
		lower := 1 - upper
		p = 2 * math.Min(lower, upper)
	case "poisson":
		lambda := d.Params[0]
		if lambda <= 0 {
			return 1
		}
		lower := poissonCDF(math.Floor(x), lambda)
		p = 2 * math.Min(lower, 1-lower)
	default:
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

func fitDistribution(x []float64) Distribution {
	n := len(x)
	if n < 4 {
		return Distribution{Family: "normal", Params: []float64{meanOf(x), 1}}
	}
	mean, sd := meanStd(x)
	allNonNeg, allPos, allInt := true, true, true
	for _, v := range x {
		if v < 0 {
			allNonNeg, allPos = false, false
		} else if v == 0 {
			allPos = false
		}
		if v != math.Trunc(v) {
			allInt = false
		}
	}

	best := Distribution{Family: "normal", Params: []float64{mean, sd}, LogLik: normalLogLik(x, mean, sd)}
	bestAIC := aic(2, best.LogLik)
	consider := func(fam string, k int, params []float64, ll float64) {
		if a := aic(k, ll); a < bestAIC {
			best, bestAIC = Distribution{Family: fam, Params: params, LogLik: ll}, a
		}
	}

	if allPos {
		var s, s2 float64
		for _, v := range x {
			l := math.Log(v)
			s += l
			s2 += l * l
		}
		lm := s / float64(n)
		lsd := math.Sqrt(math.Max(0, s2/float64(n)-lm*lm))
		if lsd > 0 {
			consider("lognormal", 2, []float64{lm, lsd}, lognormalLogLik(x, lm, lsd))
		}
	}
	if allNonNeg && mean > 0 {
		rate := 1 / mean
		consider("exponential", 1, []float64{rate}, exponentialLogLik(x, rate))
	}
	if allNonNeg && allInt && mean > 0 {
		consider("poisson", 1, []float64{mean}, poissonLogLik(x, mean))
	}
	return best
}

func aic(k int, logLik float64) float64 {
	return 2*float64(k) - 2*logLik
}

func normalLogLik(x []float64, mu, sd float64) float64 {
	if sd <= 0 {
		return math.Inf(-1)
	}
	var ll float64
	c := -0.5*math.Log(2*math.Pi) - math.Log(sd)
	for _, v := range x {
		z := (v - mu) / sd
		ll += c - 0.5*z*z
	}
	return ll
}

func lognormalLogLik(x []float64, mu, sd float64) float64 {
	if sd <= 0 {
		return math.Inf(-1)
	}
	var ll float64
	c := -0.5*math.Log(2*math.Pi) - math.Log(sd)
	for _, v := range x {
		if v <= 0 {
			return math.Inf(-1)
		}
		lv := math.Log(v)
		z := (lv - mu) / sd
		ll += c - 0.5*z*z - lv
	}
	return ll
}

func exponentialLogLik(x []float64, rate float64) float64 {
	if rate <= 0 {
		return math.Inf(-1)
	}
	var ll float64
	logRate := math.Log(rate)
	for _, v := range x {
		ll += logRate - rate*v
	}
	return ll
}

func poissonLogLik(x []float64, lambda float64) float64 {
	if lambda <= 0 {
		return math.Inf(-1)
	}
	var ll float64
	logLambda := math.Log(lambda)
	for _, v := range x {
		k := math.Round(v)
		ll += k*logLambda - lambda - logGamma(k+1)
	}
	return ll
}

func poissonCDF(k, lambda float64) float64 {
	if k < 0 {
		return 0
	}
	if lambda > 500 {
		return 0.5 * math.Erfc(-((k+0.5)-lambda)/(math.Sqrt(lambda)*math.Sqrt2))
	}
	var sum, term float64
	term = math.Exp(-lambda)
	sum = term
	for i := 1.0; i <= k; i++ {
		term *= lambda / i
		sum += term
	}
	if sum > 1 {
		sum = 1
	}
	return sum
}

func logGamma(x float64) float64 {
	v, _ := math.Lgamma(x)
	return v
}
