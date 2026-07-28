package sub

import (
	"fmt"
	"math"
	"runtime"
	"sync"

	"github.com/urfan03/semeion/outlier"
)

type Options struct {
	Window  int
	Stride  int
	K       int
	Raw     bool
	Spread  bool
	Workers int
}

func (o Options) resolve(n int) Options {
	if o.Window <= 0 {
		o.Window = AutoWindow(n)
	}
	if o.Stride <= 0 {
		o.Stride = 1
	}
	if o.K <= 0 {
		o.K = 5
	}
	if o.Workers <= 0 {
		o.Workers = runtime.GOMAXPROCS(0)
	}
	return o
}

func (o Options) embed(t []float64) Embedding {
	e := Embed(t, o.Window, o.Stride)
	if !o.Raw {
		e = e.ZNormalize()
	}
	return e
}

type neighbourhood struct {
	dists [][]float64
	idx   [][]int
	kdist []float64
}

func nearest(e Embedding, k, workers int) neighbourhood {
	n := len(e.Rows)
	nb := neighbourhood{
		dists: make([][]float64, n),
		idx:   make([][]int, n),
		kdist: make([]float64, n),
	}
	if n == 0 {
		return nb
	}
	if k > n-1 {
		k = n - 1
	}
	if k < 1 {
		k = 1
	}
	excl := e.Window
	if excl < 1 {
		excl = 1
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			topD := make([]float64, k)
			topI := make([]int, k)
			for i := w; i < n; i += workers {
				for j := range topD {
					topD[j] = math.Inf(1)
					topI[j] = -1
				}
				for j := 0; j < n; j++ {
					if abs(e.Starts[i]-e.Starts[j]) < excl {
						continue
					}
					d := euclid(e.Rows[i], e.Rows[j])
					if d >= topD[k-1] {
						continue
					}
					p := k - 1
					for p > 0 && topD[p-1] > d {
						topD[p], topI[p] = topD[p-1], topI[p-1]
						p--
					}
					topD[p], topI[p] = d, j
				}
				ds := make([]float64, 0, k)
				is := make([]int, 0, k)
				for j := 0; j < k; j++ {
					if topI[j] >= 0 {
						ds = append(ds, topD[j])
						is = append(is, topI[j])
					}
				}
				nb.dists[i], nb.idx[i] = ds, is
				if len(ds) > 0 {
					nb.kdist[i] = ds[len(ds)-1]
				}
			}
		}(w)
	}
	wg.Wait()
	return nb
}

func KNN(t []float64, opt Options) []float64 {
	opt = opt.resolve(len(t))
	e := opt.embed(t)
	if len(e.Rows) < 3 {
		return make([]float64, len(t))
	}
	nb := nearest(e, opt.K, opt.Workers)
	scores := make([]float64, len(e.Rows))
	for i, ds := range nb.dists {
		if len(ds) == 0 {
			continue
		}
		var sum float64
		for _, d := range ds {
			sum += d
		}
		scores[i] = sum / float64(len(ds))
	}
	return e.Scatter(scores, opt.Spread)
}

func LOF(t []float64, opt Options) []float64 {
	opt = opt.resolve(len(t))
	e := opt.embed(t)
	n := len(e.Rows)
	if n < 3 {
		return make([]float64, len(t))
	}
	nb := nearest(e, opt.K, opt.Workers)

	lrd := make([]float64, n)
	for i := 0; i < n; i++ {
		if len(nb.idx[i]) == 0 {
			continue
		}
		var sum float64
		for p, j := range nb.idx[i] {
			sum += math.Max(nb.kdist[j], nb.dists[i][p])
		}
		mean := sum / float64(len(nb.idx[i]))
		if mean <= 1e-12 {
			lrd[i] = math.Inf(1)
			continue
		}
		lrd[i] = 1 / mean
	}

	scores := make([]float64, n)
	for i := 0; i < n; i++ {
		if len(nb.idx[i]) == 0 || math.IsInf(lrd[i], 1) {
			continue
		}
		var sum float64
		for _, j := range nb.idx[i] {
			if math.IsInf(lrd[j], 1) {
				sum += 1 / 1e-12
				continue
			}
			sum += lrd[j]
		}
		scores[i] = sum / float64(len(nb.idx[i])) / lrd[i]
		if math.IsNaN(scores[i]) || math.IsInf(scores[i], 0) {
			scores[i] = 0
		}
	}
	return e.Scatter(scores, opt.Spread)
}

func Population(t []float64, opt Options) ([]float64, error) {
	opt = opt.resolve(len(t))
	e := opt.embed(t)
	if len(e.Rows) < 3 {
		return make([]float64, len(t)), nil
	}
	if len(e.Rows) > outlier.MaxRows {
		return nil, fmt.Errorf("%d subsequences exceeds the population detector limit of %d — raise Stride", len(e.Rows), outlier.MaxRows)
	}
	features := make([]string, e.Window)
	for i := range features {
		features[i] = fmt.Sprintf("lag%d", e.Window-1-i)
	}
	res, err := outlier.Detect(features, e.Rows, outlier.Options{K: opt.K, Raw: true})
	if err != nil {
		return nil, err
	}
	scores := make([]float64, len(e.Rows))
	for _, r := range res {
		if r.Index >= 0 && r.Index < len(scores) {
			scores[r.Index] = r.Score
		}
	}
	return e.Scatter(scores, opt.Spread), nil
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
