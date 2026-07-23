package outlier

import (
	"fmt"
	"math"
	"sort"
)

const MaxRows = 50000

type Result struct {
	Index int `json:"index"`

	Score float64 `json:"score"`

	Methods map[string]float64 `json:"methods"`

	Influence map[string]float64 `json:"influence,omitempty"`
}

type Options struct {
	K int

	Raw bool
}

func (o Options) k(n int) int {
	k := o.K
	if k <= 0 {
		k = 5
	}
	if k > n-1 {
		k = n - 1
	}
	if k < 1 {
		k = 1
	}
	return k
}

func Detect(features []string, rows [][]float64, opt Options) ([]Result, error) {
	n := len(rows)
	if n < 3 {
		return nil, fmt.Errorf("outlier detection needs at least 3 rows, got %d", n)
	}
	if n > MaxRows {
		return nil, fmt.Errorf("outlier detection is limited to %d rows, got %d — sample first", MaxRows, n)
	}
	d := len(features)
	for i, r := range rows {
		if len(r) != d {
			return nil, fmt.Errorf("row %d has %d values, expected %d", i, len(r), d)
		}
	}

	x := rows
	if !opt.Raw {
		x = standardize(rows, d)
	}
	k := opt.k(n)

	nbrs := neighbours(x, k)

	knn := make([]float64, n)
	kth := make([]float64, n)
	lof := make([]float64, n)
	ldof := make([]float64, n)
	for i := range x {
		knn[i] = mean(nbrs[i].dists)
		kth[i] = nbrs[i].dists[len(nbrs[i].dists)-1]
		ldof[i] = ldofOf(x, nbrs[i])
	}
	lofAll(x, nbrs, lof)

	out := make([]Result, n)
	nk := normalize(knn)
	nt := normalize(kth)
	nl := normalize(lof)
	nd := normalize(ldof)
	for i := range x {
		m := map[string]float64{"knn": nk[i], "kth_nn": nt[i], "lof": nl[i], "ldof": nd[i]}
		out[i] = Result{
			Index:     i,
			Score:     (nk[i] + nt[i] + nl[i] + nd[i]) / 4,
			Methods:   m,
			Influence: influence(x[i], x, nbrs[i], features),
		}
	}
	return out, nil
}

func Top(res []Result, n int) []Result {
	sorted := append([]Result(nil), res...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Score > sorted[j].Score })
	if n > 0 && n < len(sorted) {
		sorted = sorted[:n]
	}
	return sorted
}

type nbr struct {
	idx   []int
	dists []float64
}

func neighbours(x [][]float64, k int) []nbr {
	n := len(x)
	out := make([]nbr, n)
	type cand struct {
		i int
		d float64
	}
	buf := make([]cand, 0, n)
	for i := range x {
		buf = buf[:0]
		for j := range x {
			if i == j {
				continue
			}
			buf = append(buf, cand{j, dist(x[i], x[j])})
		}
		sort.Slice(buf, func(a, b int) bool { return buf[a].d < buf[b].d })
		nb := nbr{idx: make([]int, k), dists: make([]float64, k)}
		for t := 0; t < k; t++ {
			nb.idx[t], nb.dists[t] = buf[t].i, buf[t].d
		}
		out[i] = nb
	}
	return out
}

func lofAll(x [][]float64, nbrs []nbr, out []float64) {
	n := len(x)
	lrd := make([]float64, n)
	for i := 0; i < n; i++ {
		var sum float64
		for t, j := range nbrs[i].idx {

			kdistJ := nbrs[j].dists[len(nbrs[j].dists)-1]
			sum += math.Max(kdistJ, nbrs[i].dists[t])
		}
		if sum <= 0 {
			lrd[i] = math.Inf(1)
			continue
		}
		lrd[i] = float64(len(nbrs[i].idx)) / sum
	}
	for i := 0; i < n; i++ {
		if math.IsInf(lrd[i], 1) {
			out[i] = 1
			continue
		}
		var sum float64
		for _, j := range nbrs[i].idx {
			if math.IsInf(lrd[j], 1) {
				continue
			}
			sum += lrd[j]
		}
		if lrd[i] <= 0 {
			out[i] = 1
			continue
		}
		out[i] = sum / (float64(len(nbrs[i].idx)) * lrd[i])
	}
}

func ldofOf(x [][]float64, nb nbr) float64 {
	k := len(nb.idx)
	if k < 2 {
		return 1
	}
	var inner float64
	pairs := 0
	for a := 0; a < k; a++ {
		for b := a + 1; b < k; b++ {
			inner += dist(x[nb.idx[a]], x[nb.idx[b]])
			pairs++
		}
	}
	if pairs == 0 || inner <= 0 {
		return 1
	}
	return mean(nb.dists) / (inner / float64(pairs))
}

func normalize(raw []float64) []float64 {
	logs := make([]float64, len(raw))
	for i, v := range raw {
		logs[i] = math.Log(math.Max(v, 1e-12))
	}
	raw = logs

	med := median(raw)
	scale := 1.4826 * medianAbsDev(raw, med)
	if scale <= 0 {

		scale = stddev(raw)
		if scale <= 0 {
			return make([]float64, len(raw))
		}
	}
	out := make([]float64, len(raw))
	for i, v := range raw {
		z := (v - med) / scale
		out[i] = 1 / (1 + math.Exp(-(z - 3)))
	}
	return out
}

func influence(row []float64, x [][]float64, nb nbr, features []string) map[string]float64 {
	if len(features) == 0 {
		return nil
	}
	acc := make([]float64, len(features))
	total := 0.0
	for f := range features {
		var s float64
		for _, j := range nb.idx {
			dv := row[f] - x[j][f]
			s += dv * dv
		}
		acc[f] = s
		total += s
	}
	if total <= 0 {
		return nil
	}
	out := make(map[string]float64, len(features))
	for f, name := range features {
		out[name] = acc[f] / total
	}
	return out
}

func standardize(rows [][]float64, d int) [][]float64 {
	col := make([]float64, len(rows))
	scales := make([]float64, d)
	centers := make([]float64, d)
	for f := 0; f < d; f++ {
		for i, r := range rows {
			col[i] = r[f]
		}
		med := median(col)
		s := 1.4826 * medianAbsDev(col, med)
		if s <= 0 {
			s = stddev(col)
		}
		if s <= 0 {
			s = 1
		}
		centers[f], scales[f] = med, s
	}
	out := make([][]float64, len(rows))
	for i, r := range rows {
		o := make([]float64, d)
		for f := 0; f < d; f++ {
			o[f] = (r[f] - centers[f]) / scales[f]
		}
		out[i] = o
	}
	return out
}

func dist(a, b []float64) float64 {
	var s float64
	for i := range a {
		d := a[i] - b[i]
		s += d * d
	}
	return math.Sqrt(s)
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	var s float64
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func stddev(v []float64) float64 {
	if len(v) < 2 {
		return 0
	}
	m := mean(v)
	var s float64
	for _, x := range v {
		s += (x - m) * (x - m)
	}
	return math.Sqrt(s / float64(len(v)-1))
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}

func medianAbsDev(v []float64, med float64) float64 {
	if len(v) == 0 {
		return 0
	}
	dev := make([]float64, len(v))
	for i, x := range v {
		dev[i] = math.Abs(x - med)
	}
	return median(dev)
}
