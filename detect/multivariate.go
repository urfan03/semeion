package detect

import "math"

// MultivariateModel detects RELATIONSHIP-BREAK anomalies: it learns the mean
// vector + covariance of a set of metrics and scores a new vector by its
// Mahalanobis distance (χ² tail on k dof). This catches "CPU up + latency up +
// throughput DOWN together" — anomalous jointly even when each metric alone is
// in range — which Elastic's independent multi-metric jobs miss. It also
// attributes the anomaly across metrics (each metric's share of the distance).
type MultivariateModel struct {
	k       int
	window  int
	warmup  int
	history [][]float64
}

// NewMultivariateModel builds a model over k metrics.
func NewMultivariateModel(k int) *MultivariateModel {
	return &MultivariateModel{k: k, window: defaultWindow, warmup: defaultWarmup}
}

// Observe scores vec (length k) against the learned joint distribution and folds
// it in. Returns the χ² tail probability, the 0..100 score, the Mahalanobis
// distance, and each metric's contribution share (0..1, ~summing to 1) to it.
func (m *MultivariateModel) Observe(vec []float64) (prob, score, dist float64, contrib []float64) {
	if len(vec) != m.k {
		return 1, 0, 0, nil
	}
	if len(m.history) < m.warmup {
		m.push(vec)
		return 1, 0, 0, nil
	}
	mean := meanVec(m.history, m.k)
	cov := covMatrix(m.history, mean)
	ridgeDiagonal(cov) // stabilise near-singular covariances
	inv, ok := invert(cov)
	if !ok {
		m.push(vec)
		return 1, 0, nil
	}
	d := make([]float64, m.k)
	for i := range d {
		d[i] = vec[i] - mean[i]
	}
	iv := matVec(inv, d) // Σ⁻¹·(x−μ)
	var m2 float64
	for i := range d {
		m2 += d[i] * iv[i]
	}
	if m2 < 0 {
		m2 = 0 // numerical guard
	}
	prob = chiSquareTail(m2, m.k)
	score = scoreFromProbability(prob)

	contrib = make([]float64, m.k)
	if m2 > 0 {
		for i := range d {
			contrib[i] = d[i] * iv[i] / m2
		}
	}
	m.push(vec)
	return prob, score, contrib
}

func (m *MultivariateModel) push(vec []float64) {
	cp := append([]float64(nil), vec...)
	m.history = append(m.history, cp)
	if len(m.history) > m.window {
		m.history = m.history[len(m.history)-m.window:]
	}
}

// ── linear algebra (small, dependency-free) ──────────────────────────────────

func meanVec(rows [][]float64, k int) []float64 {
	mean := make([]float64, k)
	for _, r := range rows {
		for i := 0; i < k; i++ {
			mean[i] += r[i]
		}
	}
	n := float64(len(rows))
	for i := range mean {
		mean[i] /= n
	}
	return mean
}

func covMatrix(rows [][]float64, mean []float64) [][]float64 {
	k := len(mean)
	cov := make([][]float64, k)
	for i := range cov {
		cov[i] = make([]float64, k)
	}
	for _, r := range rows {
		for i := 0; i < k; i++ {
			for j := 0; j < k; j++ {
				cov[i][j] += (r[i] - mean[i]) * (r[j] - mean[j])
			}
		}
	}
	n := float64(len(rows))
	for i := 0; i < k; i++ {
		for j := 0; j < k; j++ {
			cov[i][j] /= n
		}
	}
	return cov
}

// ridgeDiagonal adds a small multiple of the average variance to the diagonal so
// collinear / low-sample covariances stay invertible (Tikhonov regularisation).
func ridgeDiagonal(cov [][]float64) {
	k := len(cov)
	var tr float64
	for i := 0; i < k; i++ {
		tr += cov[i][i]
	}
	eps := 1e-6*tr/float64(k) + 1e-9
	for i := 0; i < k; i++ {
		cov[i][i] += eps
	}
}

// invert returns the inverse of a square matrix via Gauss-Jordan with partial
// pivoting (false if singular).
func invert(a [][]float64) ([][]float64, bool) {
	n := len(a)
	m := make([][]float64, n)
	for i := range m {
		m[i] = make([]float64, 2*n)
		copy(m[i], a[i])
		m[i][n+i] = 1
	}
	for col := 0; col < n; col++ {
		piv := col
		for r := col + 1; r < n; r++ {
			if math.Abs(m[r][col]) > math.Abs(m[piv][col]) {
				piv = r
			}
		}
		if math.Abs(m[piv][col]) < 1e-12 {
			return nil, false
		}
		m[col], m[piv] = m[piv], m[col]
		d := m[col][col]
		for j := 0; j < 2*n; j++ {
			m[col][j] /= d
		}
		for r := 0; r < n; r++ {
			if r == col {
				continue
			}
			f := m[r][col]
			for j := 0; j < 2*n; j++ {
				m[r][j] -= f * m[col][j]
			}
		}
	}
	inv := make([][]float64, n)
	for i := range inv {
		inv[i] = append([]float64(nil), m[i][n:]...)
	}
	return inv, true
}

func matVec(a [][]float64, x []float64) []float64 {
	n := len(a)
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		var s float64
		for j := 0; j < n; j++ {
			s += a[i][j] * x[j]
		}
		out[i] = s
	}
	return out
}

// ── χ² tail via the regularised upper incomplete gamma Q(k/2, x/2) ───────────

func chiSquareTail(x float64, k int) float64 {
	if x <= 0 || k <= 0 {
		return 1
	}
	return gammaQ(float64(k)/2, x/2)
}

// gammaQ is the regularised upper incomplete gamma Q(a,x)=1-P(a,x)
// (Numerical Recipes: series for x<a+1, continued fraction otherwise).
func gammaQ(a, x float64) float64 {
	if x < 0 || a <= 0 {
		return 1
	}
	if x < a+1 {
		return 1 - gammaSeries(a, x)
	}
	return gammaCF(a, x)
}

func gammaSeries(a, x float64) float64 {
	if x <= 0 {
		return 0
	}
	gln, _ := math.Lgamma(a)
	ap := a
	sum := 1.0 / a
	del := sum
	for n := 0; n < 200; n++ {
		ap++
		del *= x / ap
		sum += del
		if math.Abs(del) < math.Abs(sum)*1e-14 {
			break
		}
	}
	return sum * math.Exp(-x+a*math.Log(x)-gln)
}

func gammaCF(a, x float64) float64 {
	gln, _ := math.Lgamma(a)
	const tiny = 1e-300
	b := x + 1 - a
	c := 1 / tiny
	d := 1 / b
	h := d
	for i := 1; i < 200; i++ {
		an := -float64(i) * (float64(i) - a)
		b += 2
		d = an*d + b
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = b + an/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < 1e-14 {
			break
		}
	}
	return math.Exp(-x+a*math.Log(x)-gln) * h
}
