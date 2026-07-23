package stats

import "math"

func CrossCorrelation(a, b []float64, maxLag int) []float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	out := make([]float64, 2*maxLag+1)
	if n < 2 {
		return out
	}
	for lag := -maxLag; lag <= maxLag; lag++ {
		out[lag+maxLag] = corrAtLag(a, b, n, lag)
	}
	return out
}

func corrAtLag(a, b []float64, n, lag int) float64 {
	var xs, ys []float64
	for t := 0; t < n; t++ {
		u := t + lag
		if u < 0 || u >= n {
			continue
		}
		xs = append(xs, a[t])
		ys = append(ys, b[u])
	}
	return pearson(xs, ys)
}

func pearson(x, y []float64) float64 {
	n := len(x)
	if n < 2 || len(y) != n {
		return 0
	}
	mx, my := meanOf(x), meanOf(y)
	var sxy, sxx, syy float64
	for i := range x {
		dx, dy := x[i]-mx, y[i]-my
		sxy += dx * dy
		sxx += dx * dx
		syy += dy * dy
	}
	if sxx == 0 || syy == 0 {
		return 0
	}
	return sxy / math.Sqrt(sxx*syy)
}

func meanOf(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	var s float64
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}

func LeadLag(a, b []float64, maxLag int) (lag int, corr float64) {
	cc := CrossCorrelation(a, b, maxLag)
	best := 0
	for i, c := range cc {
		if math.Abs(c) > math.Abs(cc[best]) {
			best = i
		}
	}
	return best - maxLag, cc[best]
}

func Granger(a, b []float64, order int) (improvement, fStat float64) {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if order < 1 || n <= 3*order+1 {
		return 0, 0
	}

	var yRows []float64
	var restr, full [][]float64
	for t := order; t < n; t++ {
		yRows = append(yRows, b[t])
		rr := []float64{1}
		fr := []float64{1}
		for k := 1; k <= order; k++ {
			rr = append(rr, b[t-k])
			fr = append(fr, b[t-k])
		}
		for k := 1; k <= order; k++ {
			fr = append(fr, a[t-k])
		}
		restr = append(restr, rr)
		full = append(full, fr)
	}
	sseR := olsSSE(restr, yRows)
	sseF := olsSSE(full, yRows)
	if sseR <= 0 {
		return 0, 0
	}
	improvement = (sseR - sseF) / sseR
	if improvement < 0 {
		improvement = 0
	}
	m := len(yRows)
	pFull := 2*order + 1
	dfDen := m - pFull
	if dfDen > 0 && sseF > 0 {
		fStat = ((sseR - sseF) / float64(order)) / (sseF / float64(dfDen))
		if fStat < 0 {
			fStat = 0
		}
	}
	return improvement, fStat
}

func olsSSE(X [][]float64, y []float64) float64 {
	m := len(X)
	if m == 0 {
		return 0
	}
	p := len(X[0])

	xtx := make([][]float64, p)
	xty := make([]float64, p)
	for i := range xtx {
		xtx[i] = make([]float64, p)
	}
	for r := 0; r < m; r++ {
		for i := 0; i < p; i++ {
			xty[i] += X[r][i] * y[r]
			for j := 0; j < p; j++ {
				xtx[i][j] += X[r][i] * X[r][j]
			}
		}
	}
	var tr float64
	for i := 0; i < p; i++ {
		tr += xtx[i][i]
	}
	ridge := 1e-8*tr/float64(p) + 1e-12
	for i := 0; i < p; i++ {
		xtx[i][i] += ridge
	}
	beta, ok := solve(xtx, xty)
	if !ok {
		my := meanOf(y)
		var tss float64
		for _, v := range y {
			tss += (v - my) * (v - my)
		}
		return tss
	}
	var sse float64
	for r := 0; r < m; r++ {
		pred := 0.0
		for i := 0; i < p; i++ {
			pred += beta[i] * X[r][i]
		}
		d := y[r] - pred
		sse += d * d
	}
	return sse
}

func solve(A [][]float64, b []float64) ([]float64, bool) {
	n := len(b)
	scale := 0.0
	for i := 0; i < n; i++ {
		if v := math.Abs(A[i][i]); v > scale {
			scale = v
		}
	}
	if scale == 0 {
		scale = 1
	}
	m := make([][]float64, n)
	for i := range m {
		m[i] = append(append([]float64(nil), A[i]...), b[i])
	}
	for col := 0; col < n; col++ {
		piv := col
		for r := col + 1; r < n; r++ {
			if math.Abs(m[r][col]) > math.Abs(m[piv][col]) {
				piv = r
			}
		}
		if math.Abs(m[piv][col]) < 1e-12*scale {
			return nil, false
		}
		m[col], m[piv] = m[piv], m[col]
		for r := 0; r < n; r++ {
			if r == col {
				continue
			}
			f := m[r][col] / m[col][col]
			for c := col; c <= n; c++ {
				m[r][c] -= f * m[col][c]
			}
		}
	}
	x := make([]float64, n)
	for i := 0; i < n; i++ {
		x[i] = m[i][n] / m[i][i]
	}
	return x, true
}
