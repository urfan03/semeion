package mp

import "math"

type DAMPOptions struct {
	Window    int
	Warmup    int
	Lookahead int
	Spread    bool
}

func (o DAMPOptions) resolve(n int) DAMPOptions {
	if o.Window <= 0 {
		o.Window = AutoWindow(n)
	}
	if o.Warmup <= 0 {
		o.Warmup = 16 * o.Window
	}
	if o.Warmup < 2*o.Window {
		o.Warmup = 2 * o.Window
	}
	if o.Lookahead < 0 {
		o.Lookahead = 0
	}
	return o
}

func minOf(xs []float64) float64 {
	best := math.Inf(1)
	for _, v := range xs {
		if v < best {
			best = v
		}
	}
	return best
}

func DAMP(t []float64, opt DAMPOptions) []float64 {
	n := len(t)
	opt = opt.resolve(n)
	m := opt.Window
	out := make([]float64, n)
	if m < 2 || n < opt.Warmup+m {
		return out
	}

	l := n - m + 1
	amp := make([]float64, l)
	pruned := make([]bool, l)
	bestSoFar := 0.0
	excl := exclusion(m)

	for i := opt.Warmup; i < l; i++ {
		if pruned[i] {
			if i > 0 {
				amp[i] = amp[i-1]
			}
			continue
		}
		query := t[i : i+m]
		limit := i - excl
		if limit < m {
			amp[i] = 0
			continue
		}

		approx := math.Inf(1)
		x := 2 * m
		first := true
		for {
			if i-x+1 < 0 {
				if d := MASS(query, t[:limit+m-1]); d != nil {
					approx = minOf(d)
				} else {
					approx = 0
				}
				break
			}
			lo := i - x + 1
			hi := limit + m - 1
			if !first {
				hi = i - x/2 + m - 1
				if hi > limit+m-1 {
					hi = limit + m - 1
				}
			}
			first = false
			if hi-lo < m {
				x *= 2
				continue
			}
			if d := MASS(query, t[lo:hi]); d != nil {
				if v := minOf(d); v < approx {
					approx = v
				}
			}
			if approx < bestSoFar {
				break
			}
			x *= 2
		}
		if math.IsInf(approx, 1) {
			approx = 0
		}
		amp[i] = approx

		if approx > bestSoFar {
			bestSoFar = approx
			if opt.Lookahead > 0 {
				hi := i + opt.Lookahead + m - 1
				if hi > n {
					hi = n
				}
				if hi-i > m {
					if d := MASS(query, t[i:hi]); d != nil {
						for k, v := range d {
							j := i + k
							if j < l && v < bestSoFar {
								pruned[j] = true
							}
						}
					}
				}
			}
		}
	}

	if opt.Spread {
		return PointScores(amp, n, m)
	}
	copy(out, amp)
	return out
}
