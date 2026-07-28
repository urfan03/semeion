package guard

import (
	"math"
	"sort"
)

type Budget struct {
	Alarms int
	Per    int
}

func (b Budget) rate() float64 {
	if b.Alarms <= 0 || b.Per <= 0 {
		return 0
	}
	return float64(b.Alarms) / float64(b.Per)
}

func SolveThreshold(scores []float64, b Budget, opt Options) float64 {
	rate := b.rate()
	if rate <= 0 || len(scores) == 0 {
		return math.Inf(1)
	}
	want := int(math.Round(rate * float64(len(scores))))
	if want < 1 {
		want = 1
	}

	candidates := make([]float64, 0, len(scores))
	for _, v := range scores {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			candidates = append(candidates, v)
		}
	}
	if len(candidates) == 0 {
		return math.Inf(1)
	}
	sort.Float64s(candidates)
	uniq := candidates[:1]
	for _, v := range candidates[1:] {
		if v != uniq[len(uniq)-1] {
			uniq = append(uniq, v)
		}
	}

	count := func(thr float64) int {
		o := opt
		o.Threshold = thr
		n := 0
		for _, f := range Apply(scores, o) {
			if f {
				n++
			}
		}
		return n
	}

	lo, hi := 0, len(uniq)-1
	best := math.Inf(1)
	for lo <= hi {
		mid := (lo + hi) / 2
		if count(uniq[mid]) <= want {
			best = uniq[mid]
			hi = mid - 1
		} else {
			lo = mid + 1
		}
	}
	if best == 0 {
		return math.Nextafter(0, math.Inf(-1))
	}
	return best
}

func WithBudget(scores []float64, b Budget, opt Options) []bool {
	o := opt
	o.Threshold = SolveThreshold(scores, b, opt)
	return Apply(scores, o)
}

type Effect struct {
	Values   []float64
	Baseline []float64
	Scale    []float64
	MinRel   float64
	MinAbs   float64
}

func (e Effect) size(i int) float64 {
	if i < 0 || i >= len(e.Values) {
		return 0
	}
	base := 0.0
	if i < len(e.Baseline) {
		base = e.Baseline[i]
	}
	return math.Abs(e.Values[i] - base)
}

func (e Effect) passes(i int) bool {
	d := e.size(i)
	if e.MinAbs > 0 && d < e.MinAbs {
		return false
	}
	if e.MinRel > 0 {
		scale := 0.0
		if i < len(e.Scale) {
			scale = math.Abs(e.Scale[i])
		}
		if scale <= 0 {
			return d > 0
		}
		if d/scale < e.MinRel {
			return false
		}
	}
	return true
}

func GateByEffect(alarms []bool, e Effect) []bool {
	out := make([]bool, len(alarms))
	for i, a := range alarms {
		out[i] = a && e.passes(i)
	}
	return out
}

func RollingBaseline(values []float64, window int) ([]float64, []float64) {
	n := len(values)
	base := make([]float64, n)
	scale := make([]float64, n)
	if window < 2 {
		window = 2
	}
	buf := make([]float64, 0, window)
	dev := make([]float64, 0, window)
	for i := 0; i < n; i++ {
		lo := i - window
		if lo < 0 {
			lo = 0
		}
		if i == 0 {
			base[i], scale[i] = values[0], 0
			continue
		}
		buf = append(buf[:0], values[lo:i]...)
		m := medianOf(buf)
		base[i] = m
		dev = dev[:0]
		for _, v := range buf {
			dev = append(dev, math.Abs(v-m))
		}
		scale[i] = 1.4826 * medianOf(dev)
	}
	return base, scale
}

func medianOf(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	m := len(c) / 2
	if len(c)%2 == 1 {
		return c[m]
	}
	return (c[m-1] + c[m]) / 2
}
