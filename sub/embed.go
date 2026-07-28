package sub

import "math"

type Embedding struct {
	Rows   [][]float64
	Starts []int
	Window int
	Length int
}

func Embed(t []float64, window, stride int) Embedding {
	n := len(t)
	if window < 2 || stride < 1 || n < window {
		return Embedding{Window: window, Length: n}
	}
	count := (n-window)/stride + 1
	rows := make([][]float64, 0, count)
	starts := make([]int, 0, count)
	for i := 0; i+window <= n; i += stride {
		row := make([]float64, window)
		copy(row, t[i:i+window])
		rows = append(rows, row)
		starts = append(starts, i)
	}
	return Embedding{Rows: rows, Starts: starts, Window: window, Length: n}
}

func (e Embedding) ZNormalize() Embedding {
	for _, row := range e.Rows {
		var sum float64
		for _, v := range row {
			sum += v
		}
		mean := sum / float64(len(row))
		var ss float64
		for _, v := range row {
			ss += (v - mean) * (v - mean)
		}
		sd := math.Sqrt(ss / float64(len(row)))
		if sd <= 1e-12 {
			for i := range row {
				row[i] = 0
			}
			continue
		}
		for i := range row {
			row[i] = (row[i] - mean) / sd
		}
	}
	return e
}

func (e Embedding) Scatter(values []float64, spread bool) []float64 {
	out := make([]float64, e.Length)
	if len(values) != len(e.Starts) {
		return out
	}
	if !spread {
		for i, s := range e.Starts {
			if values[i] > out[s] {
				out[s] = values[i]
			}
		}
		return out
	}
	for i, s := range e.Starts {
		for k := 0; k < e.Window && s+k < e.Length; k++ {
			if values[i] > out[s+k] {
				out[s+k] = values[i]
			}
		}
	}
	return out
}

func AutoWindow(n int) int {
	m := 16
	if 2*m > n {
		m = n / 2
	}
	if m < 2 {
		return 0
	}
	return m
}

func euclid(a, b []float64) float64 {
	var s float64
	for i := range a {
		d := a[i] - b[i]
		s += d * d
	}
	return math.Sqrt(s)
}
