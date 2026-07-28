package sub

import "math"

type PCAOptions struct {
	Options
	Components int
	Variance   float64
}

func jacobiEigen(a [][]float64) ([]float64, [][]float64) {
	n := len(a)
	m := make([][]float64, n)
	for i := range m {
		m[i] = append([]float64(nil), a[i]...)
	}
	v := make([][]float64, n)
	for i := range v {
		v[i] = make([]float64, n)
		v[i][i] = 1
	}
	for sweep := 0; sweep < 100; sweep++ {
		var off float64
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				off += m[i][j] * m[i][j]
			}
		}
		if off <= 1e-24 {
			break
		}
		for p := 0; p < n; p++ {
			for q := p + 1; q < n; q++ {
				if math.Abs(m[p][q]) < 1e-18 {
					continue
				}
				theta := (m[q][q] - m[p][p]) / (2 * m[p][q])
				t := 1 / (math.Abs(theta) + math.Sqrt(theta*theta+1))
				if theta < 0 {
					t = -t
				}
				c := 1 / math.Sqrt(t*t+1)
				s := t * c
				for k := 0; k < n; k++ {
					mkp, mkq := m[k][p], m[k][q]
					m[k][p] = c*mkp - s*mkq
					m[k][q] = s*mkp + c*mkq
				}
				for k := 0; k < n; k++ {
					mpk, mqk := m[p][k], m[q][k]
					m[p][k] = c*mpk - s*mqk
					m[q][k] = s*mpk + c*mqk
				}
				for k := 0; k < n; k++ {
					vkp, vkq := v[k][p], v[k][q]
					v[k][p] = c*vkp - s*vkq
					v[k][q] = s*vkp + c*vkq
				}
			}
		}
	}
	vals := make([]float64, n)
	for i := 0; i < n; i++ {
		vals[i] = m[i][i]
	}
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	for i := 1; i < n; i++ {
		for j := i; j > 0 && vals[order[j]] > vals[order[j-1]]; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	sortedVals := make([]float64, n)
	vecs := make([][]float64, n)
	for c, o := range order {
		sortedVals[c] = vals[o]
		vec := make([]float64, n)
		for r := 0; r < n; r++ {
			vec[r] = v[r][o]
		}
		vecs[c] = vec
	}
	return sortedVals, vecs
}

func PCA(t []float64, opt PCAOptions) []float64 {
	opt.Options = opt.Options.resolve(len(t))
	e := opt.embed(t)
	rows := e.Rows
	n := len(rows)
	if n < 3 {
		return make([]float64, len(t))
	}
	d := e.Window

	mean := make([]float64, d)
	for _, row := range rows {
		for j, v := range row {
			mean[j] += v
		}
	}
	for j := range mean {
		mean[j] /= float64(n)
	}

	cov := make([][]float64, d)
	for i := range cov {
		cov[i] = make([]float64, d)
	}
	for _, row := range rows {
		for i := 0; i < d; i++ {
			di := row[i] - mean[i]
			for j := i; j < d; j++ {
				cov[i][j] += di * (row[j] - mean[j])
			}
		}
	}
	den := float64(n - 1)
	for i := 0; i < d; i++ {
		for j := i; j < d; j++ {
			cov[i][j] /= den
			cov[j][i] = cov[i][j]
		}
	}

	vals, vecs := jacobiEigen(cov)
	var total float64
	for _, v := range vals {
		if v > 0 {
			total += v
		}
	}
	k := opt.Components
	if k <= 0 {
		want := opt.Variance
		if want <= 0 || want >= 1 {
			want = 0.9
		}
		var acc float64
		for i, v := range vals {
			if v > 0 {
				acc += v
			}
			k = i + 1
			if total > 0 && acc/total >= want {
				break
			}
		}
	}
	if k > d {
		k = d
	}
	if k < 1 {
		k = 1
	}

	scores := make([]float64, n)
	centred := make([]float64, d)
	proj := make([]float64, k)
	for i, row := range rows {
		for j := 0; j < d; j++ {
			centred[j] = row[j] - mean[j]
		}
		for c := 0; c < k; c++ {
			var dot float64
			for j := 0; j < d; j++ {
				dot += centred[j] * vecs[c][j]
			}
			proj[c] = dot
		}
		var err float64
		for j := 0; j < d; j++ {
			var rec float64
			for c := 0; c < k; c++ {
				rec += proj[c] * vecs[c][j]
			}
			diff := centred[j] - rec
			err += diff * diff
		}
		scores[i] = math.Sqrt(err)
	}
	return e.Scatter(scores, opt.Spread)
}
