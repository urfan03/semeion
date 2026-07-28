package mp

import "math"

func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

func fft(re, im []float64, inverse bool) {
	n := len(re)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		ang := 2 * math.Pi / float64(length)
		if !inverse {
			ang = -ang
		}
		wRe, wIm := math.Cos(ang), math.Sin(ang)
		for i := 0; i < n; i += length {
			curRe, curIm := 1.0, 0.0
			half := length / 2
			for j := 0; j < half; j++ {
				uRe, uIm := re[i+j], im[i+j]
				vRe := re[i+j+half]*curRe - im[i+j+half]*curIm
				vIm := re[i+j+half]*curIm + im[i+j+half]*curRe
				re[i+j], im[i+j] = uRe+vRe, uIm+vIm
				re[i+j+half], im[i+j+half] = uRe-vRe, uIm-vIm
				curRe, curIm = curRe*wRe-curIm*wIm, curRe*wIm+curIm*wRe
			}
		}
	}
	if inverse {
		fn := float64(n)
		for i := range re {
			re[i] /= fn
			im[i] /= fn
		}
	}
}

func slidingDot(query, series []float64) []float64 {
	m, n := len(query), len(series)
	if m == 0 || n < m {
		return nil
	}
	size := nextPow2(n + m)
	qRe := make([]float64, size)
	qIm := make([]float64, size)
	for i, v := range query {
		qRe[m-1-i] = v
	}
	sRe := make([]float64, size)
	sIm := make([]float64, size)
	copy(sRe, series)

	fft(qRe, qIm, false)
	fft(sRe, sIm, false)
	for i := 0; i < size; i++ {
		re := qRe[i]*sRe[i] - qIm[i]*sIm[i]
		im := qRe[i]*sIm[i] + qIm[i]*sRe[i]
		qRe[i], qIm[i] = re, im
	}
	fft(qRe, qIm, true)

	out := make([]float64, n-m+1)
	for i := range out {
		out[i] = qRe[i+m-1]
	}
	return out
}

func MASS(query, series []float64) []float64 {
	m := len(query)
	if m < 2 || len(series) < m {
		return nil
	}
	var qs, qss float64
	for _, v := range query {
		qs += v
		qss += v * v
	}
	fm := float64(m)
	qMean := qs / fm
	qVar := qss/fm - qMean*qMean
	if qVar < 0 {
		qVar = 0
	}
	qStd := math.Sqrt(qVar)

	mu, sig := meanStd(series, m)
	qt := slidingDot(query, series)
	out := make([]float64, len(qt))
	maxDist := math.Sqrt(2 * fm)
	for i := range qt {
		if qStd <= 0 || sig[i] <= 0 {
			if qStd <= 0 && sig[i] <= 0 {
				out[i] = 0
			} else {
				out[i] = maxDist
			}
			continue
		}
		corr := (qt[i] - fm*qMean*mu[i]) / (fm * qStd * sig[i])
		if corr > 1 {
			corr = 1
		} else if corr < -1 {
			corr = -1
		}
		d := 2 * fm * (1 - corr)
		if d < 0 {
			d = 0
		}
		out[i] = math.Sqrt(d)
	}
	return out
}
