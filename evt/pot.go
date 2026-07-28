package evt

import (
	"math"
	"sort"
)

type GPD struct {
	Gamma float64
	Sigma float64
}

type Options struct {
	Q          float64
	Level      float64
	MinPeaks   int
	MaxPeaks   int
	RefitEvery int
}

func (o Options) withDefaults() Options {
	if o.Q <= 0 || o.Q >= 1 {
		o.Q = 1e-5
	}
	if o.Level <= 0 || o.Level >= 1 {
		o.Level = 0.98
	}
	if o.MinPeaks <= 0 {
		o.MinPeaks = 10
	}
	if o.MaxPeaks <= 0 {
		o.MaxPeaks = 2000
	}
	if o.RefitEvery <= 0 {
		o.RefitEvery = 1
	}
	return o
}

func profile(peaks []float64, theta float64) (GPD, float64, bool) {
	n := float64(len(peaks))
	var s float64
	for _, y := range peaks {
		z := 1 + theta*y
		if z <= 0 {
			return GPD{}, 0, false
		}
		s += math.Log(z)
	}
	gamma := s / n
	if gamma == 0 {
		return GPD{}, 0, false
	}
	sigma := gamma / theta
	if sigma <= 0 || math.IsNaN(sigma) || math.IsInf(sigma, 0) {
		return GPD{}, 0, false
	}
	ll := -n*math.Log(sigma) - n*gamma - n
	if math.IsNaN(ll) || math.IsInf(ll, 0) {
		return GPD{}, 0, false
	}
	return GPD{Gamma: gamma, Sigma: sigma}, ll, true
}

func candidates(ymax, mean float64) []float64 {
	lo := -1 / ymax
	out := make([]float64, 0, 96)
	const k = 32
	for i := 1; i < k; i++ {
		out = append(out, lo*(float64(i)/float64(k)))
	}
	for e := 1; e <= 8; e++ {
		out = append(out, lo*(1-math.Pow(10, -float64(e))))
	}
	for e := -16; e <= 8; e++ {
		out = append(out, math.Pow(10, float64(e)/2)/mean)
	}
	sort.Float64s(out)
	return out
}

func FitGPD(peaks []float64) (GPD, bool) {
	ys := make([]float64, 0, len(peaks))
	var sum, ymax float64
	for _, y := range peaks {
		if y <= 0 || math.IsNaN(y) || math.IsInf(y, 0) {
			continue
		}
		ys = append(ys, y)
		sum += y
		if y > ymax {
			ymax = y
		}
	}
	n := len(ys)
	if n < 5 || ymax <= 0 || sum <= 0 {
		return GPD{}, false
	}
	mean := sum / float64(n)
	best := GPD{Gamma: 0, Sigma: mean}
	bestLL := -float64(n)*math.Log(mean) - float64(n)

	cs := candidates(ymax, mean)
	bestIdx := -1
	for i, th := range cs {
		g, ll, ok := profile(ys, th)
		if ok && ll > bestLL {
			bestLL, best, bestIdx = ll, g, i
		}
	}
	if bestIdx >= 0 {
		lo, hi := cs[bestIdx], cs[bestIdx]
		if bestIdx > 0 {
			lo = cs[bestIdx-1]
		}
		if bestIdx < len(cs)-1 {
			hi = cs[bestIdx+1]
		}
		if g, ll, ok := refine(ys, lo, hi); ok && ll > bestLL {
			bestLL, best = ll, g
		}
	}
	if math.IsNaN(best.Sigma) || best.Sigma <= 0 {
		return GPD{}, false
	}
	return best, true
}

func refine(peaks []float64, lo, hi float64) (GPD, float64, bool) {
	const phi = 0.6180339887498949
	if hi <= lo {
		return GPD{}, 0, false
	}
	eval := func(th float64) (GPD, float64, bool) {
		if th == 0 {
			return GPD{}, math.Inf(-1), false
		}
		return profile(peaks, th)
	}
	a, b := lo, hi
	c := b - phi*(b-a)
	d := a + phi*(b-a)
	_, fc, _ := eval(c)
	_, fd, _ := eval(d)
	for i := 0; i < 40; i++ {
		if fc > fd {
			b, d, fd = d, c, fc
			c = b - phi*(b-a)
			_, fc, _ = eval(c)
		} else {
			a, c, fc = c, d, fd
			d = a + phi*(b-a)
			_, fd, _ = eval(d)
		}
		if math.Abs(b-a) < 1e-14*(math.Abs(a)+math.Abs(b)+1e-300) {
			break
		}
	}
	return eval((a + b) / 2)
}

func Quantile(g GPD, t, q float64, n, nt int) float64 {
	if nt <= 0 || n <= 0 || q <= 0 || g.Sigma <= 0 {
		return t
	}
	r := q * float64(n) / float64(nt)
	if r <= 0 {
		return t
	}
	if math.Abs(g.Gamma) < 1e-10 {
		return t - g.Sigma*math.Log(r)
	}
	z := t + (g.Sigma/g.Gamma)*(math.Pow(r, -g.Gamma)-1)
	if math.IsNaN(z) || math.IsInf(z, 0) {
		return t
	}
	return z
}

func quantileOf(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[n-1]
	}
	pos := p * float64(n-1)
	i := int(pos)
	if i >= n-1 {
		return sorted[n-1]
	}
	frac := pos - float64(i)
	return sorted[i] + frac*(sorted[i+1]-sorted[i])
}

func POT(data []float64, opt Options) (float64, GPD, bool) {
	opt = opt.withDefaults()
	clean := make([]float64, 0, len(data))
	for _, v := range data {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			clean = append(clean, v)
		}
	}
	if len(clean) < opt.MinPeaks*2 {
		return 0, GPD{}, false
	}
	sorted := append([]float64(nil), clean...)
	sort.Float64s(sorted)
	t := quantileOf(sorted, opt.Level)
	peaks := make([]float64, 0, len(clean)/10)
	for _, v := range clean {
		if v > t {
			peaks = append(peaks, v-t)
		}
	}
	if len(peaks) < opt.MinPeaks {
		return 0, GPD{}, false
	}
	g, ok := FitGPD(peaks)
	if !ok {
		return 0, GPD{}, false
	}
	return Quantile(g, t, opt.Q, len(clean), len(peaks)), g, true
}
