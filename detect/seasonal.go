package detect

import (
	"time"

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
	span   time.Duration // bucket span, for timestamp→bucket-index phase

	history []float64 // recent values
	histBkt []int64   // absolute bucket index of each history value (parallel)
	idx     int       // count of observations seen

	period      int
	phases      []*Model
	sinceDetect int
}

// NewSeasonalModel builds a seasonality-aware model. span is the job's bucket
// span, used to derive a value's phase from its TIMESTAMP (not its arrival
// order), so a missing/late bucket no longer shifts every later phase baseline.
func NewSeasonalModel(side jobspec.Side, prov model.Provider, span time.Duration) *SeasonalModel {
	if prov == nil {
		prov = model.NewGoProvider()
	}
	if span <= 0 {
		span = time.Minute
	}
	return &SeasonalModel{side: side, prov: prov, global: NewModel(side), span: span}
}

// bucketIndex maps a timestamp to an absolute bucket number, so phase =
// bucketIndex % period is anchored to wall-clock time.
func (m *SeasonalModel) bucketIndex(t time.Time) int64 {
	return t.UnixNano() / int64(m.span)
}

// Observe scores value (at bucket time t) against its phase baseline (or the
// global baseline before a period is known), then folds it in.
func (m *SeasonalModel) Observe(t time.Time, value float64) (prob, score, typical float64, dir core.Direction) {
	m.idx++
	m.sinceDetect++
	bkt := m.bucketIndex(t)

	if len(m.history) >= seasonalMinHistory &&
		(m.period == 0 && m.sinceDetect >= 1 || m.sinceDetect >= seasonalRedetect) {
		m.detect()
	}

	if m.period >= 2 && len(m.phases) == m.period {
		ph := int(((bkt % int64(m.period)) + int64(m.period)) % int64(m.period))
		prob, score, typical, dir = m.phases[ph].Observe(value)
	} else {
		prob, score, typical, dir = m.global.Observe(value)
	}

	m.history = append(m.history, value)
	m.histBkt = append(m.histBkt, bkt)
	if len(m.history) > seasonalMaxHistory {
		drop := len(m.history) - seasonalMaxHistory
		m.history = m.history[drop:]
		m.histBkt = m.histBkt[drop:]
	}
	return prob, score, typical, dir
}

// detect runs period discovery on the accumulated history and, on a new period,
// rebuilds the phase baselines by replaying history into them — using each
// value's absolute bucket index for its phase, so gaps don't misalign phases.
func (m *SeasonalModel) detect() {
	m.sinceDetect = 0
	periods := m.prov.DetectSeasonality(m.history)
	if len(periods) == 0 {
		return
	}
	p := pickPeriod(periods, len(m.history))
	if p < 2 || p > len(m.history)/3 || p == m.period {
		return
	}
	m.period = p
	m.phases = make([]*Model, p)
	for i := range m.phases {
		m.phases[i] = NewModelWarmup(m.side, defaultWindow, seasonalPhaseWarmup)
	}
	for j, v := range m.history {
		ph := int(((m.histBkt[j] % int64(p)) + int64(p)) % int64(p))
		m.phases[ph].Learn(v)
	}
}

// pickPeriod chooses which detected period the model keys its phases on: the
// strongest (shortest prominent) one. Detection returns multiple peaks, but the
// longer ones are usually just harmonics of the base cycle, not independent
// seasonalities — preferring a multiple would fragment a single cycle into
// copies. Genuine multi-seasonality (daily AND weekly modelled together) needs a
// multi-component decomposition and is deliberately out of scope here; the extra
// detected periods are surfaced for reporting, not phase modelling.
func pickPeriod(periods []int, histLen int) int {
	if len(periods) == 0 {
		return 0
	}
	return periods[0]
}

// Count is how many observations have been seen.
func (m *SeasonalModel) Count() int { return m.idx }

// Period returns the currently detected period (0 if none yet).
func (m *SeasonalModel) Period() int { return m.period }
