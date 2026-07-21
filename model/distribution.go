package model

import "math"

// Distribution is a fitted parametric model of a series' values. Anomaly
// scoring can use its two-sided tail probability instead of a robust z-score,
// which fits skewed / count data far better than a Gaussian assumption.
type Distribution struct {
	Family string    `json:"family"` // "normal" | "lognormal" | "exponential" | "poisson"
	Params []float64 `json:"params"` // family-specific parameters
	LogLik float64   `json:"loglik"` // maximised log-likelihood (for model choice)
}

// Tail returns the two-sided extremeness probability of x under the fitted
// distribution: p = 2·min(P(X≤x), P(X≥x)), clamped to (0,1]. Smaller = more
// extreme. An empty/unknown distribution returns 1 (not anomalous).
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
		// Right-skewed with its mode at 0, so only the UPPER tail is anomalous.
		rate := d.Params[0]
		if rate <= 0 {
			return 1
		}
		if x < 0 {
			return 1
		}
		p = math.Exp(-rate * x) // survival P(X >= x); x=0 → 1
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

// fitDistribution selects the max-likelihood distribution among the candidates
// applicable to the samples.
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

	if allPos {
		// lognormal: fit on ln(x)
		var s, s2 float64
		for _, v := range x {
			l := math.Log(v)
			s += l
			s2 += l * l
		}
		lm := s / float64(n)
		lsd := math.Sqrt(math.Max(0, s2/float64(n)-lm*lm))
		if lsd > 0 {
			ll := lognormalLogLik(x, lm, lsd)
			if ll > best.LogLik {
				best = Distribution{Family: "lognormal", Params: []float64{lm, lsd}, LogLik: ll}
			}
		}
	}
	if allNonNeg && mean > 0 {
		rate := 1 / mean
		ll := exponentialLogLik(x, rate)
		if ll > best.LogLik {
			best = Distribution{Family: "exponential", Params: []float64{rate}, LogLik: ll}
		}
	}
	if allNonNeg && allInt && mean > 0 {
		ll := poissonLogLik(x, mean)
		if ll > best.LogLik {
			best = Distribution{Family: "poisson", Params: []float64{mean}, LogLik: ll}
		}
	}
	return best
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

// poissonCDF returns P(X <= k) for a Poisson(lambda), summing terms (exact for
// small k; a normal approximation for large lambda keeps it O(1)).
func poissonCDF(k, lambda float64) float64 {
	if k < 0 {
		return 0
	}
	if lambda > 500 { // normal approximation with continuity correction
		return 0.5 * math.Erfc(-((k+0.5)-lambda)/(math.Sqrt(lambda)*math.Sqrt2))
	}
	var sum, term float64
	term = math.Exp(-lambda) // P(X=0)
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

// logGamma is math.Lgamma without the sign return, for Poisson log-factorial.
func logGamma(x float64) float64 {
	v, _ := math.Lgamma(x)
	return v
}
