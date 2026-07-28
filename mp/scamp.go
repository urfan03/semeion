package mp

import (
	"math"
	"runtime"
	"sync"
)

func Parallel(t []float64, m int, workers int) []float64 {
	return scamp(t, m, false, workers)
}

func scamp(t []float64, m int, constMatch bool, workers int) []float64 {
	n := len(t)
	if m < 2 || n < 2*m {
		return nil
	}
	l := n - m + 1
	mu, sig := meanStd(t, m)
	fm := float64(m)
	excl := exclusion(m)
	eps := flatEpsilon(t)
	maxDist := math.Sqrt(2 * fm)

	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	if workers > l {
		workers = l
	}
	if workers < 1 {
		workers = 1
	}

	diagonals := make([]int, 0, l)
	for k := excl; k < l; k++ {
		diagonals = append(diagonals, k)
	}
	if len(diagonals) == 0 {
		return make([]float64, l)
	}

	locals := make([][]float64, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		local := make([]float64, l)
		for i := range local {
			local[i] = math.Inf(1)
		}
		locals[w] = local
		wg.Add(1)
		go func(w int, local []float64) {
			defer wg.Done()
			for di := w; di < len(diagonals); di += workers {
				k := diagonals[di]
				var qt float64
				for x := 0; x < m; x++ {
					qt += t[x] * t[k+x]
				}
				for i := 0; k+i < l; i++ {
					if i > 0 {
						qt += t[i+m-1]*t[k+i+m-1] - t[i-1]*t[k+i-1]
					}
					j := k + i
					var d float64
					switch {
					case constMatch && sig[i] <= eps && sig[j] <= eps:
						d = 0
					case constMatch && (sig[i] <= eps || sig[j] <= eps):
						d = maxDist
					default:
						d = dist(qt, mu[i], mu[j], sig[i], sig[j], fm)
					}
					if d < local[i] {
						local[i] = d
					}
					if d < local[j] {
						local[j] = d
					}
				}
			}
		}(w, local)
	}
	wg.Wait()

	out := make([]float64, l)
	for i := range out {
		best := math.Inf(1)
		for _, local := range locals {
			if local[i] < best {
				best = local[i]
			}
		}
		if math.IsInf(best, 1) {
			best = 0
		}
		out[i] = best
	}
	return out
}
