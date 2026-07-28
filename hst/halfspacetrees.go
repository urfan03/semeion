package hst

import (
	"math"
	"math/rand/v2"
)

type Options struct {
	Trees      int
	Height     int
	WindowSize int
	SizeLimit  float64
	Seed       uint64
}

func (o Options) withDefaults() Options {
	if o.Trees <= 0 {
		o.Trees = 25
	}
	if o.Height <= 0 {
		o.Height = 8
	}
	if o.Height > 24 {
		o.Height = 24
	}
	if o.WindowSize <= 0 {
		o.WindowSize = 250
	}
	if o.SizeLimit <= 0 {
		o.SizeLimit = 0.1 * float64(o.WindowSize)
	}
	if o.Seed == 0 {
		o.Seed = 0x5eed
	}
	return o
}

type node struct {
	dim   int
	split float64
	left  *node
	right *node
	ref   float64
	cur   float64
}

type Forest struct {
	opt    Options
	dims   int
	trees  []*node
	count  int
	warm   bool
	maxRaw float64
}

func New(dims int, opt Options) *Forest {
	opt = opt.withDefaults()
	if dims < 1 {
		dims = 1
	}
	rng := rand.New(rand.NewPCG(opt.Seed, opt.Seed^0x9e3779b97f4a7c15))
	f := &Forest{opt: opt, dims: dims}
	mins := make([]float64, dims)
	maxs := make([]float64, dims)
	for i := 0; i < opt.Trees; i++ {
		for q := 0; q < dims; q++ {
			s := rng.Float64()
			w := 2 * math.Max(s, 1-s)
			mins[q] = s - w
			maxs[q] = s + w
		}
		f.trees = append(f.trees, build(mins, maxs, 0, opt.Height, rng))
	}
	f.maxRaw = float64(opt.WindowSize) * (math.Pow(2, float64(opt.Height)+1) - 1)
	return f
}

func build(mins, maxs []float64, depth, height int, rng *rand.Rand) *node {
	n := &node{dim: -1}
	if depth >= height {
		return n
	}
	q := rng.IntN(len(mins))
	p := (mins[q] + maxs[q]) / 2
	n.dim, n.split = q, p

	hi := maxs[q]
	maxs[q] = p
	n.left = build(mins, maxs, depth+1, height, rng)
	maxs[q] = hi

	lo := mins[q]
	mins[q] = p
	n.right = build(mins, maxs, depth+1, height, rng)
	mins[q] = lo
	return n
}

func at(x []float64, i int) float64 {
	if i < 0 || i >= len(x) {
		return 0
	}
	return x[i]
}

func (f *Forest) Dims() int { return f.dims }

func (f *Forest) Warm() bool { return f.warm }

func (f *Forest) Score(x []float64) float64 {
	if !f.warm || f.maxRaw <= 0 || len(f.trees) == 0 {
		return 0
	}
	var total float64
	for _, t := range f.trees {
		total += scoreTree(t, x, f.opt.SizeLimit)
	}
	s := 1 - (total/float64(len(f.trees)))/f.maxRaw
	if s < 0 {
		return 0
	}
	if s > 1 {
		return 1
	}
	return s
}

func scoreTree(n *node, x []float64, limit float64) float64 {
	var sum float64
	for depth := 0; n != nil; depth++ {
		sum += n.ref * float64(uint64(1)<<uint(depth))
		if n.dim < 0 || n.ref < limit {
			break
		}
		if at(x, n.dim) < n.split {
			n = n.left
		} else {
			n = n.right
		}
	}
	return sum
}

func (f *Forest) Learn(x []float64) {
	for _, t := range f.trees {
		for n := t; n != nil; {
			n.cur++
			if n.dim < 0 {
				break
			}
			if at(x, n.dim) < n.split {
				n = n.left
			} else {
				n = n.right
			}
		}
	}
	f.count++
	if f.count >= f.opt.WindowSize {
		for _, t := range f.trees {
			swap(t)
		}
		f.count = 0
		f.warm = true
	}
}

func swap(n *node) {
	if n == nil {
		return
	}
	n.ref, n.cur = n.cur, 0
	swap(n.left)
	swap(n.right)
}

func (f *Forest) Update(x []float64) float64 {
	s := f.Score(x)
	f.Learn(x)
	return s
}

type Scaler struct {
	min  []float64
	max  []float64
	out  []float64
	seen bool
}

func NewScaler(dims int) *Scaler {
	if dims < 1 {
		dims = 1
	}
	return &Scaler{min: make([]float64, dims), max: make([]float64, dims), out: make([]float64, dims)}
}

func (s *Scaler) Transform(x []float64) []float64 {
	for i := range s.out {
		v := at(x, i)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			v = 0
		}
		if !s.seen || v < s.min[i] {
			s.min[i] = v
		}
		if !s.seen || v > s.max[i] {
			s.max[i] = v
		}
		r := s.max[i] - s.min[i]
		if r <= 0 {
			s.out[i] = 0.5
		} else {
			s.out[i] = (v - s.min[i]) / r
		}
	}
	s.seen = true
	return s.out
}

type SeriesOptions struct {
	Options
	Lags int
	Diff bool
}

func Series(values []float64, opt SeriesOptions) []float64 {
	if len(values) == 0 {
		return nil
	}
	lags := opt.Lags
	if lags <= 0 {
		lags = 1
	}
	dims := lags
	if opt.Diff {
		dims++
	}
	f := New(dims, opt.Options)
	sc := NewScaler(dims)
	feat := make([]float64, dims)
	out := make([]float64, len(values))
	for i := range values {
		for l := 0; l < lags; l++ {
			j := i - l
			if j < 0 {
				j = 0
			}
			feat[l] = values[j]
		}
		if opt.Diff {
			var d float64
			if i > 0 {
				d = values[i] - values[i-1]
			}
			feat[lags] = d
		}
		out[i] = f.Update(sc.Transform(feat))
	}
	return out
}
