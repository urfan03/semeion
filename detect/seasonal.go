package detect

import (
	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/model"
)

// Seasonal detection tuning.
const (
	seasonalMinHistory  = 40   // observations before the first period detection
	seasonalRedetect    = 300  // re-run period detection every N observations
	seasonalPhaseWarmup = 4    // per-phase warm-up (phases see 1 sample/period)
	seasonalMaxHistory  = 6000 // bounded history for detection + replay
)

// SeasonalModel is a seasonality-aware baseline: once a period is discovered
// (via the model.Provider), it keeps a separate baseline per phase (index mod
// period), so a value is scored against what's normal FOR ITS TIME-OF-CYCLE —
// catching a daytime trough or a midnight spike that a global baseline misses.
// Until a period is found it behaves like a plain Model.
type SeasonalModel struct {
	side   jobspec.Side
	prov   model.Provider
	global *Model

	history   []float64
	histStart int // absolute index of history[0]
	idx       int // count of observations seen

	period      int
	phases      []*Model
	sinceDetect int
}

// NewSeasonalModel builds a seasonality-aware model using the given provider.
func NewSeasonalModel(side jobspec.Side, prov model.Provider) *SeasonalModel {
	if prov == nil {
		prov = model.NewGoProvider()
	}
	return &SeasonalModel{side: side, prov: prov, global: NewModel(side)}
}

// Observe scores value against the phase baseline (or the global baseline before
// a period is known), then folds it in.
func (m *SeasonalModel) Observe(value float64) (prob, score, typical float64, dir core.Direction) {
	cur := m.idx // 0-based index of this observation
	m.idx++
	m.sinceDetect++

	// (Re)discover the period periodically, once there's enough history.
	if len(m.history) >= seasonalMinHistory &&
		(m.period == 0 && m.sinceDetect >= 1 || m.sinceDetect >= seasonalRedetect) {
		m.detect()
	}

	if m.period >= 2 && len(m.phases) == m.period {
		ph := cur % m.period
		prob, score, typical, dir = m.phases[ph].Observe(value)
	} else {
		prob, score, typical, dir = m.global.Observe(value)
	}

	m.history = append(m.history, value)
	if len(m.history) > seasonalMaxHistory {
		drop := len(m.history) - seasonalMaxHistory
		m.history = m.history[drop:]
		m.histStart += drop
	}
	return prob, score, typical, dir
}

// detect runs period discovery on the accumulated history and, on a new period,
// rebuilds the phase baselines by replaying history into them.
func (m *SeasonalModel) detect() {
	m.sinceDetect = 0
	periods := m.prov.DetectSeasonality(m.history)
	if len(periods) == 0 {
		return
	}
	p := periods[0]
	if p < 2 || p > len(m.history)/3 || p == m.period {
		return
	}
	m.period = p
	m.phases = make([]*Model, p)
	for i := range m.phases {
		m.phases[i] = NewModelWarmup(m.side, defaultWindow, seasonalPhaseWarmup)
	}
	for j, v := range m.history {
		ph := (m.histStart + j) % p
		m.phases[ph].Learn(v)
	}
}

// Count is how many observations have been seen.
func (m *SeasonalModel) Count() int { return m.idx }

// Period returns the currently detected period (0 if none yet).
func (m *SeasonalModel) Period() int { return m.period }
