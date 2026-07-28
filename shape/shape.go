package shape

import (
	"math"
	"sort"
)

type Kind string

const (
	Spike      Kind = "spike"
	Dip        Kind = "dip"
	LevelUp    Kind = "level_up"
	LevelDown  Kind = "level_down"
	Variance   Kind = "variance"
	TrendBreak Kind = "trend_break"
	Gap        Kind = "gap"
	Unknown    Kind = "unknown"
)

type Options struct {
	Context  int
	MinZ     float64
	MinLevel float64
	MinVar   float64
	MinGap   int
}

func (o Options) resolve(length int) Options {
	if o.Context < 4 {
		o.Context = 4 * length
		if o.Context < 30 {
			o.Context = 30
		}
	}
	if o.MinZ <= 0 {
		o.MinZ = 3
	}
	if o.MinLevel <= 0 {
		o.MinLevel = 0.5
	}
	if o.MinVar <= 0 {
		o.MinVar = 2
	}
	if o.MinGap < 2 {
		o.MinGap = 10
	}
	return o
}

type Result struct {
	Kind     Kind    `json:"kind"`
	Z        float64 `json:"z"`
	Duration int     `json:"duration"`
	Before   float64 `json:"before"`
	During   float64 `json:"during"`
	After    float64 `json:"after"`
	Spread   float64 `json:"spread_ratio"`
	Slope    float64 `json:"slope_change"`
}

func median(xs []float64) float64 {
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

func mad(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	m := median(xs)
	dev := make([]float64, len(xs))
	for i, v := range xs {
		dev[i] = math.Abs(v - m)
	}
	return 1.4826 * median(dev)
}

func slope(xs []float64) float64 {
	n := len(xs)
	if n < 2 {
		return 0
	}
	var sx, sy, sxx, sxy float64
	for i, v := range xs {
		x := float64(i)
		sx += x
		sy += v
		sxx += x * x
		sxy += x * v
	}
	fn := float64(n)
	den := fn*sxx - sx*sx
	if den == 0 {
		return 0
	}
	return (fn*sxy - sx*sy) / den
}

func Classify(values []float64, start, end int, opt Options) Result {
	if start < 0 || end < start || end >= len(values) {
		return Result{Kind: Unknown}
	}
	length := end - start + 1
	opt = opt.resolve(length)

	lo := start - opt.Context
	if lo < 0 {
		lo = 0
	}
	hi := end + opt.Context
	if hi > len(values)-1 {
		hi = len(values) - 1
	}
	before := values[lo:start]
	during := values[start : end+1]
	after := values[end+1 : hi+1]
	if len(before) < 4 {
		return Result{Kind: Unknown, Duration: length}
	}

	baseMed, baseScale := median(before), mad(before)
	res := Result{
		Duration: length,
		Before:   baseMed,
		During:   median(during),
	}
	if len(after) >= 4 {
		res.After = median(after)
	} else {
		res.After = res.During
	}

	flat := true
	for _, v := range during {
		if v != during[0] {
			flat = false
			break
		}
	}
	if flat && baseScale > 0 && math.Abs(during[0]-baseMed) > opt.MinZ*baseScale && length >= opt.MinGap {
		res.Kind = Gap
		res.Z = math.Abs(during[0]-baseMed) / baseScale
		return res
	}

	if baseScale <= 0 {
		baseScale = math.Abs(baseMed) * 1e-6
		if baseScale <= 0 {
			baseScale = 1e-9
		}
	}
	res.Z = (res.During - baseMed) / baseScale
	duringScale := mad(during)
	if baseScale > 0 {
		res.Spread = duringScale / baseScale
	}
	res.Slope = slope(after) - slope(before)

	if math.Abs(res.Z) < opt.MinZ {
		if res.Spread >= opt.MinVar {
			res.Kind = Variance
			return res
		}
		if baseScale > 0 && math.Abs(res.Slope)*float64(len(after)) > opt.MinZ*baseScale {
			res.Kind = TrendBreak
			return res
		}
		res.Kind = Unknown
		return res
	}

	shifted := math.Abs(res.After-baseMed) >= opt.MinLevel*math.Abs(res.During-baseMed)
	switch {
	case shifted && res.Z > 0:
		res.Kind = LevelUp
	case shifted:
		res.Kind = LevelDown
	case res.Z > 0:
		res.Kind = Spike
	default:
		res.Kind = Dip
	}
	return res
}

func Transient(k Kind) bool { return k == Spike || k == Dip }

func Persistent(k Kind) bool {
	return k == LevelUp || k == LevelDown || k == TrendBreak || k == Gap
}
