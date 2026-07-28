package selector

import (
	"encoding/json"
	"math"
	"sort"
)

type Example struct {
	Key      string    `json:"key,omitempty"`
	Features Features  `json:"features"`
	Best     string    `json:"best"`
	Scores   []float64 `json:"scores,omitempty"`
}

type Model struct {
	K        int       `json:"k"`
	Fallback string    `json:"fallback"`
	Examples []Example `json:"examples"`
	Mean     []float64 `json:"mean,omitempty"`
	Scale    []float64 `json:"scale,omitempty"`
}

func New(k int, fallback string) *Model {
	if k < 1 {
		k = 3
	}
	return &Model{K: k, Fallback: fallback}
}

func (m *Model) Add(key string, f Features, best string) {
	m.Examples = append(m.Examples, Example{Key: key, Features: f, Best: best})
}

func (m *Model) Fit() {
	d := len(FeatureNames())
	m.Mean = make([]float64, d)
	m.Scale = make([]float64, d)
	if len(m.Examples) == 0 {
		for i := range m.Scale {
			m.Scale[i] = 1
		}
		return
	}
	for _, e := range m.Examples {
		for i, v := range e.Features.Vector() {
			m.Mean[i] += v
		}
	}
	n := float64(len(m.Examples))
	for i := range m.Mean {
		m.Mean[i] /= n
	}
	for _, e := range m.Examples {
		for i, v := range e.Features.Vector() {
			d := v - m.Mean[i]
			m.Scale[i] += d * d
		}
	}
	for i := range m.Scale {
		m.Scale[i] = math.Sqrt(m.Scale[i] / n)
		if m.Scale[i] < 1e-9 {
			m.Scale[i] = 1
		}
	}
}

func (m *Model) normalize(f Features) []float64 {
	v := f.Vector()
	if len(m.Mean) != len(v) || len(m.Scale) != len(v) {
		return v
	}
	out := make([]float64, len(v))
	for i := range v {
		out[i] = (v[i] - m.Mean[i]) / m.Scale[i]
	}
	return out
}

func (m *Model) Predict(f Features) string {
	if len(m.Examples) == 0 {
		return m.Fallback
	}
	q := m.normalize(f)
	type cand struct {
		d    float64
		best string
	}
	cands := make([]cand, 0, len(m.Examples))
	for _, e := range m.Examples {
		x := m.normalize(e.Features)
		var s float64
		for i := range q {
			d := q[i] - x[i]
			s += d * d
		}
		cands = append(cands, cand{math.Sqrt(s), e.Best})
	}
	sort.SliceStable(cands, func(a, b int) bool { return cands[a].d < cands[b].d })
	k := m.K
	if k > len(cands) {
		k = len(cands)
	}

	votes := make(map[string]float64)
	for _, c := range cands[:k] {
		votes[c.best] += 1 / (1 + c.d)
	}
	names := make([]string, 0, len(votes))
	for n := range votes {
		names = append(names, n)
	}
	sort.Strings(names)
	best, bestVote := m.Fallback, 0.0
	for _, n := range names {
		if votes[n] > bestVote {
			best, bestVote = n, votes[n]
		}
	}
	return best
}

func (m *Model) Without(key string) *Model {
	out := New(m.K, m.Fallback)
	for _, e := range m.Examples {
		if e.Key == key {
			continue
		}
		out.Examples = append(out.Examples, e)
	}
	out.Fit()
	return out
}

func (m *Model) MarshalJSON() ([]byte, error) {
	type alias Model
	return json.Marshal((*alias)(m))
}

func (m *Model) UnmarshalJSON(b []byte) error {
	type alias Model
	if err := json.Unmarshal(b, (*alias)(m)); err != nil {
		return err
	}
	if m.K < 1 {
		m.K = 3
	}
	if len(m.Mean) == 0 || len(m.Scale) == 0 {
		m.Fit()
	}
	return nil
}
