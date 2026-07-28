package peer

import (
	"math"
	"sort"
)

type Options struct {
	Window int
	Lag    int
	Causal bool
	MinNum int
}

func (o Options) resolve() Options {
	if o.Window < 4 {
		o.Window = 100
	}
	if o.Lag < 0 {
		o.Lag = 0
	}
	if o.MinNum < 2 {
		o.MinNum = 2
	}
	return o
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

func Normalize(rows [][]float64, window int) [][]float64 {
	if window < 4 {
		window = 100
	}
	out := make([][]float64, len(rows))
	for s, series := range rows {
		z := make([]float64, len(series))
		buf := make([]float64, 0, window)
		dev := make([]float64, 0, window)
		for i := range series {
			lo := i - window
			if lo < 0 {
				lo = 0
			}
			if i-lo < 4 {
				continue
			}
			buf = append(buf[:0], series[lo:i]...)
			med := medianOf(buf)
			dev = dev[:0]
			for _, v := range buf {
				dev = append(dev, math.Abs(v-med))
			}
			scale := 1.4826 * medianOf(dev)
			if scale <= 0 {
				if series[i] != med {
					z[i] = math.Inf(1)
					if series[i] < med {
						z[i] = math.Inf(-1)
					}
				}
				continue
			}
			z[i] = (series[i] - med) / scale
		}
		out[s] = z
	}
	return out
}

func Deviation(rows [][]float64, minNum int) [][]float64 {
	m := len(rows)
	out := make([][]float64, m)
	if m == 0 {
		return out
	}
	if minNum < 2 {
		minNum = 2
	}
	n := len(rows[0])
	for _, r := range rows {
		if len(r) < n {
			n = len(r)
		}
	}
	for s := range out {
		out[s] = make([]float64, n)
	}
	if m < minNum {
		return out
	}

	col := make([]float64, 0, m)
	dev := make([]float64, 0, m)
	for i := 0; i < n; i++ {
		col = col[:0]
		for _, r := range rows {
			v := r[i]
			if math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			col = append(col, v)
		}
		if len(col) < minNum {
			continue
		}
		med := medianOf(append(col[:0:0], col...))
		dev = dev[:0]
		for _, v := range col {
			dev = append(dev, math.Abs(v-med))
		}
		scale := 1.4826 * medianOf(dev)
		for s, r := range rows {
			v := r[i]
			if math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			if scale <= 0 {
				if v != med {
					out[s][i] = math.Inf(1)
				}
				continue
			}
			out[s][i] = math.Abs(v-med) / scale
		}
	}
	return out
}

func crossMedian(rows [][]float64, n, minNum int) []float64 {
	out := make([]float64, n)
	col := make([]float64, 0, len(rows))
	for i := 0; i < n; i++ {
		col = col[:0]
		for _, r := range rows {
			if i >= len(r) {
				continue
			}
			v := r[i]
			if math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			col = append(col, v)
		}
		if len(col) < minNum {
			out[i] = math.NaN()
			continue
		}
		out[i] = medianOf(append(col[:0:0], col...))
	}
	return out
}

func Relative(rows [][]float64, opt Options) [][]float64 {
	opt = opt.resolve()
	m := len(rows)
	out := make([][]float64, m)
	if m == 0 {
		return out
	}
	n := len(rows[0])
	for _, r := range rows {
		if len(r) < n {
			n = len(r)
		}
	}
	for s := range out {
		out[s] = make([]float64, n)
	}
	if m < opt.MinNum {
		return out
	}

	med := crossMedian(rows, n, opt.MinNum)
	ratios := make([][]float64, m)
	for s := range rows {
		r := make([]float64, n)
		for i := 0; i < n; i++ {
			if math.IsNaN(med[i]) {
				r[i] = math.NaN()
				continue
			}
			if math.Abs(med[i]) > 1e-12 {
				r[i] = rows[s][i] / med[i]
				continue
			}
			r[i] = rows[s][i] - med[i]
		}
		ratios[s] = r
	}
	return Normalize(ratios, opt.Window)
}

func Scores(rows [][]float64, opt Options) [][]float64 {
	return Relative(rows, opt)
}

func windowMin(p []float64, i, back, forward int) float64 {
	lo := i - back
	if lo < 0 {
		lo = 0
	}
	hi := i + forward
	if hi >= len(p) {
		hi = len(p) - 1
	}
	best := math.Inf(1)
	for k := lo; k <= hi; k++ {
		if math.IsNaN(p[k]) {
			continue
		}
		if p[k] < best {
			best = p[k]
		}
	}
	if math.IsInf(best, 1) {
		return 1
	}
	return best
}

func Corroborate(target []float64, others [][]float64, opt Options) [][]float64 {
	opt = opt.resolve()
	n := len(target)
	out := make([][]float64, 0, len(others)+1)
	out = append(out, target)
	back, forward := opt.Lag, opt.Lag
	if opt.Causal {
		forward = 0
	}
	span := back + forward + 1
	for _, o := range others {
		conf := make([]float64, n)
		for i := 0; i < n; i++ {
			if i >= len(o) {
				conf[i] = 1
				continue
			}
			p := windowMin(o, i, back, forward)
			conf[i] = 1 - math.Pow(1-p, float64(span))
			if conf[i] > 1 {
				conf[i] = 1
			}
			if conf[i] <= 0 {
				conf[i] = 1e-300
			}
		}
		out = append(out, conf)
	}
	return out
}
