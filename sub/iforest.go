package sub

import (
	"math"
	"math/rand/v2"
)

type ForestOptions struct {
	Options
	Trees      int
	SampleSize int
	Seed       uint64
}

func (o ForestOptions) resolveForest() ForestOptions {
	if o.Trees <= 0 {
		o.Trees = 100
	}
	if o.SampleSize <= 0 {
		o.SampleSize = 256
	}
	if o.Seed == 0 {
		o.Seed = 0x1f07e57
	}
	return o
}

type iNode struct {
	dim   int
	split float64
	left  *iNode
	right *iNode
	size  int
}

func harmonicPath(n int) float64 {
	if n <= 1 {
		return 0
	}
	fn := float64(n)
	return 2*(math.Log(fn-1)+0.5772156649015329) - 2*(fn-1)/fn
}

func buildITree(rows [][]float64, idx []int, depth, limit int, rng *rand.Rand) *iNode {
	if depth >= limit || len(idx) <= 1 {
		return &iNode{dim: -1, size: len(idx)}
	}
	d := len(rows[idx[0]])
	dims := rng.Perm(d)
	for _, q := range dims {
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, i := range idx {
			v := rows[i][q]
			if v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
		if hi-lo <= 1e-12 {
			continue
		}
		p := lo + rng.Float64()*(hi-lo)
		var left, right []int
		for _, i := range idx {
			if rows[i][q] < p {
				left = append(left, i)
			} else {
				right = append(right, i)
			}
		}
		if len(left) == 0 || len(right) == 0 {
			continue
		}
		return &iNode{
			dim:   q,
			split: p,
			left:  buildITree(rows, left, depth+1, limit, rng),
			right: buildITree(rows, right, depth+1, limit, rng),
			size:  len(idx),
		}
	}
	return &iNode{dim: -1, size: len(idx)}
}

func pathLength(n *iNode, x []float64, depth int) float64 {
	for n != nil {
		if n.dim < 0 {
			return float64(depth) + harmonicPath(n.size)
		}
		if x[n.dim] < n.split {
			n = n.left
		} else {
			n = n.right
		}
		depth++
	}
	return float64(depth)
}

func IForest(t []float64, opt ForestOptions) []float64 {
	opt.Options = opt.Options.resolve(len(t))
	opt = opt.resolveForest()
	e := opt.embed(t)
	rows := e.Rows
	n := len(rows)
	if n < 3 {
		return make([]float64, len(t))
	}
	sample := opt.SampleSize
	if sample > n {
		sample = n
	}
	limit := int(math.Ceil(math.Log2(float64(sample))))
	if limit < 1 {
		limit = 1
	}

	rng := rand.New(rand.NewPCG(opt.Seed, opt.Seed^0x9e3779b97f4a7c15))
	trees := make([]*iNode, 0, opt.Trees)
	for i := 0; i < opt.Trees; i++ {
		perm := rng.Perm(n)[:sample]
		trees = append(trees, buildITree(rows, perm, 0, limit, rng))
	}

	c := harmonicPath(sample)
	if c <= 0 {
		c = 1
	}
	scores := make([]float64, n)
	for i, row := range rows {
		var sum float64
		for _, tr := range trees {
			sum += pathLength(tr, row, 0)
		}
		scores[i] = math.Pow(2, -(sum/float64(len(trees)))/c)
	}
	return e.Scatter(scores, opt.Spread)
}
