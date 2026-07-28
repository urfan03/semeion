package prep

import (
	"math"
	"sort"

	"github.com/urfan03/semeion/model"
)

type Options struct {
	Period    int
	MinPeriod int
	MaxPeriod int
	MinCycles int
	Strength  float64
	STL       bool
	Cycles    int
	Warmup    int
}

func (o Options) resolve(n int) Options {
	if o.MinPeriod < 2 {
		o.MinPeriod = 4
	}
	if o.MaxPeriod <= o.MinPeriod {
		o.MaxPeriod = 600
	}
	if o.MinCycles < 2 {
		o.MinCycles = 3
	}
	if o.Strength <= 0 {
		o.Strength = 0.2
	}
	if o.Cycles < 2 {
		o.Cycles = 8
	}
	return o
}

func DetectPeriod(values []float64, opt Options) (int, float64) {
	n := len(values)
	opt = opt.resolve(n)
	if n < opt.MinCycles*opt.MinPeriod {
		return 0, 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(n)
	var variance float64
	for _, v := range values {
		d := v - mean
		variance += d * d
	}
	variance /= float64(n)
	if variance <= 1e-18 {
		return 0, 0
	}

	maxLag := n / opt.MinCycles
	if maxLag > opt.MaxPeriod {
		maxLag = opt.MaxPeriod
	}
	best, bestLag := 0.0, 0
	prev, prevPrev := 0.0, 0.0
	for lag := 1; lag <= maxLag; lag++ {
		var s float64
		for i := lag; i < n; i++ {
			s += (values[i] - mean) * (values[i-lag] - mean)
		}
		r := s / (float64(n) * variance)
		if lag-1 >= opt.MinPeriod && prev > prevPrev && prev >= r && prev > best {
			best, bestLag = prev, lag-1
		}
		prevPrev, prev = prev, r
	}
	if best < 0 {
		best = 0
	}
	return bestLag, best
}

func Deseasonalize(values []float64, opt Options) ([]float64, int) {
	n := len(values)
	opt = opt.resolve(n)
	period := opt.Period
	if period <= 0 {
		p, strength := DetectPeriod(values, opt)
		if p <= 0 || strength < opt.Strength {
			return append([]float64(nil), values...), 0
		}
		period = p
	}
	if period < opt.MinPeriod || n < opt.MinCycles*period {
		return append([]float64(nil), values...), 0
	}
	if opt.STL {
		d := model.GoProvider{}.Decompose(values, period)
		if len(d.Resid) != n {
			return append([]float64(nil), values...), 0
		}
		out := make([]float64, n)
		copy(out, d.Resid)
		for i, v := range out {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				out[i] = 0
			}
		}
		return out, period
	}
	return causalResidual(values, period, opt.Cycles), period
}

func causalResidual(values []float64, period, cycles int) []float64 {
	n := len(values)
	out := make([]float64, n)
	hist := make([][]float64, period)
	buf := make([]float64, 0, cycles)
	for i, v := range values {
		slot := i % period
		h := hist[slot]
		if len(h) >= 2 {
			buf = append(buf[:0], h...)
			out[i] = v - medianOf(buf)
		}
		if len(h) == cycles {
			copy(h, h[1:])
			h = h[:len(h)-1]
		}
		hist[slot] = append(h, v)
	}
	return out
}

func medianOf(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sort.Float64s(xs)
	m := len(xs) / 2
	if len(xs)%2 == 1 {
		return xs[m]
	}
	return (xs[m-1] + xs[m]) / 2
}

func Abs(values []float64) []float64 {
	out := make([]float64, len(values))
	for i, v := range values {
		out[i] = math.Abs(v)
	}
	return out
}
