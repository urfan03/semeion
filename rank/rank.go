package rank

import (
	"encoding/json"
	"fmt"
	"math"
)

type Features struct {
	LogScore    float64 `json:"log_score"`
	Effect      float64 `json:"effect"`
	LogDuration float64 `json:"log_duration"`
	Agreement   float64 `json:"agreement"`
	Persistent  float64 `json:"persistent"`
	Seasonality float64 `json:"seasonality"`
	Noise       float64 `json:"noise"`
	ChangeNear  float64 `json:"change_near"`
	PeerBacked  float64 `json:"peer_backed"`
}

func (f Features) Vector() []float64 {
	return []float64{f.LogScore, f.Effect, f.LogDuration, f.Agreement, f.Persistent,
		f.Seasonality, f.Noise, f.ChangeNear, f.PeerBacked}
}

func Names() []string {
	return []string{"log_score", "effect", "log_duration", "agreement", "persistent",
		"seasonality", "noise", "change_near", "peer_backed"}
}

// Directions says which way each feature should push the verdict. A model that
// learns "a bigger effect means less likely real" is fitting noise, so those
// weights are clamped rather than trusted.
func Directions() []int {
	return []int{+1, +1, +1, +1, +1, 0, -1, +1, +1}
}

type Model struct {
	W    []float64 `json:"w"`
	B    float64   `json:"b"`
	Seen int       `json:"seen"`
	Rate float64   `json:"rate"`
	L2   float64   `json:"l2"`
}

func New() *Model {
	return &Model{W: make([]float64, len(Names())), Rate: 0.05, L2: 1e-4}
}

func (m *Model) resolve() {
	if len(m.W) != len(Names()) {
		w := make([]float64, len(Names()))
		copy(w, m.W)
		m.W = w
	}
	if m.Rate <= 0 {
		m.Rate = 0.05
	}
	if m.L2 < 0 {
		m.L2 = 0
	}
}

func sigmoid(z float64) float64 {
	if z >= 0 {
		return 1 / (1 + math.Exp(-z))
	}
	e := math.Exp(z)
	return e / (1 + e)
}

func (m *Model) Score(f Features) float64 {
	m.resolve()
	z := m.B
	for i, v := range f.Vector() {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		z += m.W[i] * v
	}
	return sigmoid(z)
}

func (m *Model) project() {
	for i, d := range Directions() {
		if i >= len(m.W) {
			break
		}
		switch {
		case d > 0 && m.W[i] < 0:
			m.W[i] = 0
		case d < 0 && m.W[i] > 0:
			m.W[i] = 0
		}
	}
}

func (m *Model) Learn(f Features, real bool) {
	m.resolve()
	y := 0.0
	if real {
		y = 1
	}
	err := m.Score(f) - y
	for i, v := range f.Vector() {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		m.W[i] -= m.Rate * (err*v + m.L2*m.W[i])
	}
	m.B -= m.Rate * err
	m.project()
	m.Seen++
}

type Example struct {
	Features Features `json:"features"`
	Real     bool     `json:"real"`
	Key      string   `json:"key,omitempty"`
}

func (m *Model) Fit(examples []Example, epochs int) {
	if epochs < 1 {
		epochs = 1
	}
	for e := 0; e < epochs; e++ {
		for _, ex := range examples {
			m.Learn(ex.Features, ex.Real)
		}
	}
}

func (m *Model) MarshalJSON() ([]byte, error) {
	m.resolve()
	type alias Model
	return json.Marshal((*alias)(m))
}

func (m *Model) UnmarshalJSON(b []byte) error {
	type alias Model
	if err := json.Unmarshal(b, (*alias)(m)); err != nil {
		return err
	}
	m.resolve()
	return nil
}

func (m *Model) Weights() map[string]float64 {
	m.resolve()
	out := make(map[string]float64, len(m.W))
	for i, n := range Names() {
		out[n] = m.W[i]
	}
	return out
}

// Threshold picks the cut on Score that keeps at most maxFalse of the labelled
// examples that are not real, preferring the cut that keeps the most real ones.
func (m *Model) Threshold(examples []Example, maxFalseRate float64) (float64, error) {
	if len(examples) == 0 {
		return 0, fmt.Errorf("rank: no examples to calibrate against")
	}
	if maxFalseRate <= 0 || maxFalseRate >= 1 {
		maxFalseRate = 0.2
	}
	best, bestKept := 1.0, -1
	for _, cut := range candidateCuts(m, examples) {
		kept, false_ := 0, 0
		for _, ex := range examples {
			if m.Score(ex.Features) < cut {
				continue
			}
			kept++
			if !ex.Real {
				false_++
			}
		}
		if kept == 0 {
			continue
		}
		if float64(false_)/float64(kept) > maxFalseRate {
			continue
		}
		if kept > bestKept {
			best, bestKept = cut, kept
		}
	}
	if bestKept < 0 {
		return 1, fmt.Errorf("rank: no cut reaches a false rate under %.2f", maxFalseRate)
	}
	return best, nil
}

func candidateCuts(m *Model, examples []Example) []float64 {
	out := make([]float64, 0, len(examples))
	for _, ex := range examples {
		out = append(out, m.Score(ex.Features))
	}
	return out
}
