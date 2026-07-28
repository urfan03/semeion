package evt

import (
	"math"
	"sort"
)

func Survival(g GPD, t, x float64, n, nt int) float64 {
	if n <= 0 || nt <= 0 || g.Sigma <= 0 || x <= t {
		return 1
	}
	rate := float64(nt) / float64(n)
	y := x - t
	var s float64
	if math.Abs(g.Gamma) < 1e-10 {
		s = math.Exp(-y / g.Sigma)
	} else {
		z := 1 + g.Gamma*y/g.Sigma
		if z <= 0 {
			return 0
		}
		s = math.Pow(z, -1/g.Gamma)
	}
	p := rate * s
	if math.IsNaN(p) {
		return 1
	}
	if p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

func (s *SPOT) Probability(x float64) float64 {
	if !s.ready || x <= s.init {
		return 1
	}
	return Survival(s.fit, s.init, x, s.n, s.nt)
}

type empirical struct {
	hist []float64
}

func (e *empirical) p(x float64) float64 {
	if len(e.hist) == 0 {
		return 1
	}
	ge := len(e.hist) - sort.SearchFloat64s(e.hist, x)
	return float64(1+ge) / float64(len(e.hist)+1)
}

func (e *empirical) add(x float64) {
	i := sort.SearchFloat64s(e.hist, x)
	e.hist = append(e.hist, 0)
	copy(e.hist[i+1:], e.hist[i:])
	e.hist[i] = x
}

func StreamProbabilities(values []float64, opt StreamOptions) []float64 {
	opt = opt.withDefaults()
	out := make([]float64, len(values))
	for i := range out {
		out[i] = 1
	}
	if len(values) <= opt.Calibration {
		return out
	}

	work := values
	if opt.Drift {
		work = residuals(values, opt.Depth)
	}
	s := NewSPOT(opt.Options)
	emp := &empirical{}
	for _, v := range work[:opt.Calibration] {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			emp.add(v)
		}
	}
	ready := s.Calibrate(work[:opt.Calibration])

	for i := opt.Calibration; i < len(work); i++ {
		x := work[i]
		if math.IsNaN(x) || math.IsInf(x, 0) {
			continue
		}
		p := emp.p(x)
		if ready && x > s.Initial() {
			if pe := s.Probability(x); pe < p {
				p = pe
			}
		}
		if p <= 0 {
			p = 1e-300
		}
		out[i] = p
		emp.add(x)
		if ready {
			s.Step(x)
		}
	}
	return out
}

func residuals(values []float64, depth int) []float64 {
	if depth < 1 {
		depth = 10
	}
	out := make([]float64, len(values))
	var sum float64
	for i := range values {
		n := i
		if n > depth {
			n = depth
		}
		if n == 0 {
			out[i] = 0
		} else {
			out[i] = values[i] - sum/float64(n)
		}
		sum += values[i]
		if i >= depth {
			sum -= values[i-depth]
		}
	}
	return out
}

func TwoSidedProbabilities(values []float64, opt StreamOptions) []float64 {
	up := StreamProbabilities(values, opt)
	neg := make([]float64, len(values))
	for i, v := range values {
		neg[i] = -v
	}
	down := StreamProbabilities(neg, opt)
	out := make([]float64, len(values))
	for i := range out {
		p := 2 * math.Min(up[i], down[i])
		if p > 1 {
			p = 1
		}
		if p <= 0 {
			p = 1e-300
		}
		out[i] = p
	}
	return out
}

func Scores(values []float64, opt StreamOptions) []float64 {
	p := StreamProbabilities(values, opt)
	out := make([]float64, len(p))
	for i, v := range p {
		if v <= 0 {
			v = 1e-300
		}
		out[i] = -math.Log10(v)
	}
	return out
}
