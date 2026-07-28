package fuse

import (
	"math"
	"sort"

	"github.com/urfan03/semeion/stats"
)

func Fisher(pvals []float64) float64 {
	var stat float64
	k := 0
	for _, p := range pvals {
		if math.IsNaN(p) {
			continue
		}
		if p <= 0 {
			p = 1e-300
		}
		if p > 1 {
			p = 1
		}
		stat += -2 * math.Log(p)
		k++
	}
	if k == 0 {
		return 1
	}
	return stats.ChiSquareTail(stat, 2*k)
}

type Tail struct {
	hist   []float64
	warmup int
}

func NewTail(warmup int) *Tail {
	if warmup < 1 {
		warmup = 1
	}
	return &Tail{warmup: warmup}
}

func (t *Tail) Step(x float64) float64 {
	p := 1.0
	if len(t.hist) >= t.warmup && !math.IsNaN(x) {
		ge := len(t.hist) - sort.SearchFloat64s(t.hist, x)
		p = float64(1+ge) / float64(len(t.hist)+1)
	}
	if !math.IsNaN(x) && !math.IsInf(x, 0) {
		i := sort.SearchFloat64s(t.hist, x)
		t.hist = append(t.hist, 0)
		copy(t.hist[i+1:], t.hist[i:])
		t.hist[i] = x
	}
	return p
}

func (t *Tail) Len() int { return len(t.hist) }

func PValues(scores []float64, warmup int) []float64 {
	out := make([]float64, len(scores))
	tail := NewTail(warmup)
	for i, s := range scores {
		out[i] = tail.Step(s)
	}
	return out
}

func Combine(streams [][]float64, warmup int) []float64 {
	if len(streams) == 0 {
		return nil
	}
	n := len(streams[0])
	for _, s := range streams {
		if len(s) < n {
			n = len(s)
		}
	}
	if n == 0 {
		return nil
	}
	tails := make([]*Tail, len(streams))
	for i := range tails {
		tails[i] = NewTail(warmup)
	}
	out := make([]float64, n)
	buf := make([]float64, len(streams))
	for i := 0; i < n; i++ {
		for j, s := range streams {
			buf[j] = tails[j].Step(s[i])
		}
		out[i] = Fisher(buf)
	}
	return out
}

func FisherStreams(pstreams [][]float64) []float64 {
	if len(pstreams) == 0 {
		return nil
	}
	n := len(pstreams[0])
	for _, s := range pstreams {
		if len(s) < n {
			n = len(s)
		}
	}
	if n == 0 {
		return nil
	}
	out := make([]float64, n)
	buf := make([]float64, len(pstreams))
	for i := 0; i < n; i++ {
		for j, s := range pstreams {
			buf[j] = s[i]
		}
		out[i] = Fisher(buf)
	}
	return out
}

func NegLog10(pvals []float64) []float64 {
	out := make([]float64, len(pvals))
	for i, v := range pvals {
		if v <= 0 {
			v = 1e-300
		}
		out[i] = -math.Log10(v)
	}
	return out
}

func CombineScores(streams [][]float64, warmup int) []float64 {
	p := Combine(streams, warmup)
	out := make([]float64, len(p))
	for i, v := range p {
		if v <= 0 {
			v = 1e-300
		}
		out[i] = -math.Log10(v)
	}
	return out
}
