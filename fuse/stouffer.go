package fuse

import "math"

func normalCDF(x float64) float64 {
	return 0.5 * math.Erfc(-x/math.Sqrt2)
}

func normalQuantile(p float64) float64 {
	if p <= 0 {
		return math.Inf(-1)
	}
	if p >= 1 {
		return math.Inf(1)
	}
	a := [6]float64{-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02,
		1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00}
	b := [5]float64{-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02,
		6.680131188771972e+01, -1.328068155288572e+01}
	c := [6]float64{-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00,
		-2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00}
	d := [4]float64{7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00,
		3.754408661907416e+00}
	const plow, phigh = 0.02425, 1 - 0.02425

	var x float64
	switch {
	case p < plow:
		q := math.Sqrt(-2 * math.Log(p))
		x = (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	case p > phigh:
		q := math.Sqrt(-2 * math.Log(1-p))
		x = -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	default:
		q := p - 0.5
		r := q * q
		x = (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q /
			(((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1)
	}
	e := normalCDF(x) - p
	u := e * math.Sqrt(2*math.Pi) * math.Exp(x*x/2)
	return x - u/(1+x*u/2)
}

func Stouffer(pvals, weights []float64) float64 {
	var num, den float64
	for i, p := range pvals {
		if math.IsNaN(p) {
			continue
		}
		if p <= 1e-300 {
			p = 1e-300
		}
		if p > 1 {
			p = 1
		}
		w := 1.0
		if i < len(weights) {
			w = weights[i]
		}
		if w <= 0 {
			continue
		}
		num += w * normalQuantile(1-p)
		den += w * w
	}
	if den <= 0 {
		return 1
	}
	z := num / math.Sqrt(den)
	if math.IsInf(z, 1) {
		return 1e-300
	}
	if math.IsInf(z, -1) {
		return 1
	}
	return clampP(1 - normalCDF(z))
}

func clampP(p float64) float64 {
	if math.IsNaN(p) || p > 1 {
		return 1
	}
	if p <= 0 {
		return 1e-300
	}
	return p
}

type Reliability struct {
	a     []float64
	b     []float64
	c     []float64
	d     []float64
	fired []float64
	total float64
	buf   []float64
	decay float64
	tau   float64
}

func NewReliability(n int, decay, tau float64) *Reliability {
	if n < 1 {
		n = 1
	}
	if decay <= 0 || decay >= 1 {
		decay = 0.999
	}
	if tau <= 0 || tau >= 1 {
		tau = 0.05
	}
	return &Reliability{
		a: make([]float64, n), b: make([]float64, n),
		c: make([]float64, n), d: make([]float64, n),
		fired: make([]float64, n), buf: make([]float64, 0, n),
		decay: decay, tau: tau,
	}
}

func (r *Reliability) Observe(pvals []float64) {
	k := len(r.a)
	r.total = r.total*r.decay + 1
	for i := 0; i < k; i++ {
		r.a[i] *= r.decay
		r.b[i] *= r.decay
		r.c[i] *= r.decay
		r.d[i] *= r.decay
		r.fired[i] *= r.decay
		if i >= len(pvals) {
			continue
		}
		r.buf = r.buf[:0]
		for j := 0; j < k && j < len(pvals); j++ {
			if j != i {
				r.buf = append(r.buf, pvals[j])
			}
		}
		ref := len(r.buf) > 0 && Fisher(r.buf) < r.tau
		fired := pvals[i] < r.tau
		if fired {
			r.fired[i]++
		}
		switch {
		case fired && ref:
			r.a[i]++
		case fired:
			r.b[i]++
		case ref:
			r.c[i]++
		default:
			r.d[i]++
		}
	}
}

func (r *Reliability) Weights() []float64 {
	out := make([]float64, len(r.a))
	for i := range out {
		tpr, fpr := 0.0, 0.0
		if r.a[i]+r.c[i] > 0 {
			tpr = r.a[i] / (r.a[i] + r.c[i])
		}
		if r.b[i]+r.d[i] > 0 {
			fpr = r.b[i] / (r.b[i] + r.d[i])
		}
		j := tpr - fpr
		if j < 0 {
			j = 0
		}
		out[i] = j * r.calibration(i)
	}
	var sum float64
	for _, v := range out {
		sum += v
	}
	if sum <= 0 {
		for i := range out {
			out[i] = 1
		}
		return out
	}
	scale := float64(len(out)) / sum
	for i := range out {
		out[i] *= scale
	}
	return out
}

func (r *Reliability) calibration(i int) float64 {
	if r.total <= 0 {
		return 1
	}
	rate := r.fired[i] / r.total
	if rate <= r.tau {
		return 1
	}
	return r.tau / rate
}

func (r *Reliability) Rate(i int) float64 {
	if r.total <= 0 || i < 0 || i >= len(r.fired) {
		return 0
	}
	return r.fired[i] / r.total
}

type WeightedOptions struct {
	Decay  float64
	Tau    float64
	Warmup int
}

func WeightedCombine(pstreams [][]float64, opt WeightedOptions) ([]float64, []float64) {
	if len(pstreams) == 0 {
		return nil, nil
	}
	n := len(pstreams[0])
	for _, s := range pstreams {
		if len(s) < n {
			n = len(s)
		}
	}
	if n == 0 {
		return nil, nil
	}
	if opt.Warmup < 0 {
		opt.Warmup = 0
	}
	rel := NewReliability(len(pstreams), opt.Decay, opt.Tau)
	out := make([]float64, n)
	buf := make([]float64, len(pstreams))
	flat := make([]float64, len(pstreams))
	for i := range flat {
		flat[i] = 1
	}
	for i := 0; i < n; i++ {
		for j, s := range pstreams {
			buf[j] = s[i]
		}
		w := flat
		if i >= opt.Warmup {
			w = rel.Weights()
		}
		out[i] = Stouffer(buf, w)
		rel.Observe(buf)
	}
	return out, rel.Weights()
}
