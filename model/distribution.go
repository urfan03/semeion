package model

import "math"

type Distribution struct {
	Family string    `json:"family"`
	Params []float64 `json:"params"`
	LogLik float64   `json:"loglik"`
}

func (d Distribution) Tail(x float64, side string) float64 {
	if d.Family == "exponential" {
		rate := d.Params[0]
		if rate <= 0 || x < 0 {
			return 1
		}
		surv := math.Exp(-rate * x)
		if side == "low" {
			return clampP(1 - surv)
		}
		return clampP(surv)
	}
	cdf, ok := d.cdf(x)
	if !ok {
		return 1
	}
	surv := 1 - cdf
	var p float64
	switch side {
	case "high":
		p = surv
	case "low":
		p = cdf
	default:
		p = 2 * math.Min(cdf, surv)
	}
	return clampP(p)
}

func (d Distribution) cdf(x float64) (float64, bool) {
	switch d.Family {
	case "normal":
		mu, sd := d.Params[0], d.Params[1]
		if sd <= 0 {
			return 0, false
		}
		return 0.5 * math.Erfc(-(x-mu)/(sd*math.Sqrt2)), true
	case "lognormal":
		mu, sd := d.Params[0], d.Params[1]
		if sd <= 0 {
			return 0, false
		}
		if x <= 0 {
			return 0, true
		}
		return 0.5 * math.Erfc(-(math.Log(x)-mu)/(sd*math.Sqrt2)), true
	case "exponential":
		rate := d.Params[0]
		if rate <= 0 {
			return 0, false
		}
		if x < 0 {
			return 0, true
		}
		return 1 - math.Exp(-rate*x), true
	case "poisson":
		lambda := d.Params[0]
		if lambda <= 0 {
			return 0, false
		}
		return poissonCDF(math.Floor(x), lambda), true
	}
	return 0, false
}

func clampP(p float64) float64 {
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

	type cand struct {
		fam    string
		k      int
		params []float64
	}
	cands := []cand{{"normal", 2, []float64{mean, sd}}}
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
			cands = append(cands, cand{"lognormal", 2, []float64{lm, lsd}})
		}
	}
	if allNonNeg && mean > 0 {
		cands = append(cands, cand{"exponential", 1, []float64{1 / mean}})
	}

	logLikOf := func(c cand) float64 {
		if allInt {
			return discreteLogLik(Distribution{Family: c.fam, Params: c.params}, x)
		}
		switch c.fam {
		case "lognormal":
			return lognormalLogLik(x, c.params[0], c.params[1])
		case "exponential":
			return exponentialLogLik(x, c.params[0])
		default:
			return normalLogLik(x, c.params[0], c.params[1])
		}
	}

	best := Distribution{Family: cands[0].fam, Params: cands[0].params, LogLik: logLikOf(cands[0])}
	bestAIC := aic(cands[0].k, best.LogLik)
	for _, c := range cands[1:] {
		ll := logLikOf(c)
		if a := aic(c.k, ll); a < bestAIC {
			best, bestAIC = Distribution{Family: c.fam, Params: c.params, LogLik: ll}, a
		}
	}
	if allNonNeg && allInt && mean > 0 {
		ll := poissonLogLik(x, mean)
		if a := aic(1, ll); a < bestAIC {
			best, bestAIC = Distribution{Family: "poisson", Params: []float64{mean}, LogLik: ll}, a
		}
	}
	return best
}

func discreteLogLik(d Distribution, x []float64) float64 {
	var ll float64
	for _, v := range x {
		hi, ok1 := d.cdf(v + 0.5)
		lo, ok2 := d.cdf(v - 0.5)
		if !ok1 || !ok2 {
			return math.Inf(-1)
		}
		pm := hi - lo
		if pm < 1e-300 {
			pm = 1e-300
		}
		ll += math.Log(pm)
	}
	return ll
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
	if lambda > 500 || k > 1000 {
		return 0.5 * math.Erfc(-((k+0.5)-lambda)/(math.Sqrt(lambda)*math.Sqrt2))
	}
	var sum, term float64
	term = math.Exp(-lambda)
	sum = term
	for i := 1.0; i <= k; i++ {
		term *= lambda / i
		sum += term
		if i > lambda && term < sum*1e-15 {
			break
		}
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
