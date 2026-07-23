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

// multiMinGain is the fraction of residual variance a second period must remove
// (beyond the single-period fit) to earn its own component — the guard that
// admits a genuine independent cycle (weekly on daily) and rejects a harmonic.
const multiMinGain = 0.10

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
	last        *Model // the phase (or global) model used by the most recent Observe

	// Multi-seasonality: when a second, independent period is found (e.g. daily
	// AND weekly), the value is modelled as level + comp1[phase1] + comp2[phase2]
	// (an additive two-component decomposition, back-fitted from history) and the
	// RESIDUAL is scored by resid. This catches an anomaly that only shows up once
	// both cycles are accounted for (a normal Monday level on the wrong hour).
	period2      int
	level        float64
	comp1        []float64
	comp2        []float64
	resid        *Model
	lastExpected float64 // expected value of the most recent multi-seasonal Observe
}

// multiActive reports whether the two-component seasonal decomposition is in use.
func (m *SeasonalModel) multiActive() bool {
	return m.period2 >= 2 && len(m.comp1) == m.period && len(m.comp2) == m.period2 && m.resid != nil
}

// Bounds returns the typical range: for the multi-seasonal path it is the
// expected value (level + both components) ± the residual scale; otherwise it is
// the phase model's own band (time-of-cycle baseline).
func (m *SeasonalModel) Bounds(z float64) (lower, upper float64) {
	if m.multiActive() {
		rl, ru := m.resid.Bounds(z)
		return m.lastExpected + rl, m.lastExpected + ru
	}
	if m.last == nil {
		return 0, 0
	}
	return m.last.Bounds(z)
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

	if m.multiActive() {
		exp := m.expected(bkt)
		m.lastExpected = exp
		prob, score, _, dir = m.resid.Observe(value - exp)
		typical = exp // report the model's expected value, not the residual baseline
	} else if m.period >= 2 && len(m.phases) == m.period {
		ph := int(((bkt % int64(m.period)) + int64(m.period)) % int64(m.period))
		m.last = m.phases[ph]
		prob, score, typical, dir = m.last.Observe(value)
	} else {
		m.last = m.global
		prob, score, typical, dir = m.last.Observe(value)
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

// detect runs period discovery on the accumulated history. When TWO independent
// periods are present (e.g. daily + weekly, not harmonics of one another) it
// builds an additive two-component decomposition and scores residuals; otherwise
// it falls back to single-period phase baselines, rebuilt by replaying history
// (each value keyed by its absolute bucket index, so gaps don't misalign phases).
func (m *SeasonalModel) detect() {
	m.sinceDetect = 0
	periods := m.prov.DetectSeasonality(m.history)
	if len(periods) == 0 {
		return
	}
	cands := candidatePeriods(periods, len(m.history))
	if len(cands) == 0 {
		return
	}
	p1 := cands[0]
	// A strong primary cycle dominates the autocorrelation and MASKS a weaker
	// second one (a big weekly sawtooth hides a smaller daily sine). So DEFLATE:
	// remove p1's seasonal component and re-run detection on the residual to
	// surface the independent second period. It then earns its own component only
	// if it materially reduces the residual variance beyond the single-period fit
	// — which both confirms it's real and rejects a mere harmonic/alias (48 on 24
	// explains ~nothing new), with no fragile integer-multiple heuristic.
	_, c1, _ := backfit(m.history, m.histBkt, p1, 0)
	resid := make([]float64, len(m.history))
	for j, v := range m.history {
		resid[j] = v - c1[phaseOf(m.histBkt[j], p1)]
	}
	cands2 := candidatePeriods(m.prov.DetectSeasonality(resid), len(m.history))
	var1 := seasonalResidualVar(m.history, m.histBkt, p1, 0)
	bestP2, bestGain := 0, 0.0
	for _, c := range cands2 {
		if c == p1 || var1 <= 0 {
			continue
		}
		gain := (var1 - seasonalResidualVar(m.history, m.histBkt, p1, c)) / var1
		if gain > bestGain {
			bestGain, bestP2 = gain, c
		}
	}
	if bestP2 >= 2 && bestGain >= multiMinGain {
		m.setupMulti(p1, bestP2)
		return
	}
	if p1 == m.period && !m.multiActive() {
		return
	}
	// Single-period phase modelling.
	m.period, m.period2 = p1, 0
	m.comp1, m.comp2, m.resid = nil, nil, nil
	m.phases = make([]*Model, p1)
	for i := range m.phases {
		m.phases[i] = NewModelWarmup(m.side, defaultWindow, seasonalPhaseWarmup)
	}
	for j, v := range m.history {
		ph := int(((m.histBkt[j] % int64(p1)) + int64(p1)) % int64(p1))
		m.phases[ph].Learn(v)
	}
}

// setupMulti back-fits the additive decomposition level + comp1 + comp2 from
// history and seeds the residual model, so a value is scored against what both
// cycles predict for its (phase1, phase2).
func (m *SeasonalModel) setupMulti(p1, p2 int) {
	if p1 == m.period && p2 == m.period2 {
		return // unchanged — keep the online-adapted residual model
	}
	m.period, m.period2 = p1, p2
	m.phases = nil
	m.level, m.comp1, m.comp2 = backfit(m.history, m.histBkt, p1, p2)
	m.resid = NewModel(m.side)
	for j, v := range m.history {
		m.resid.Learn(v - m.expectedAt(m.histBkt[j]))
	}
}

// expected is the model's predicted value for a bucket index (level + both
// seasonal components at their phases).
func (m *SeasonalModel) expected(bkt int64) float64 { return m.expectedAt(bkt) }

func (m *SeasonalModel) expectedAt(bkt int64) float64 {
	e := m.level
	if n := len(m.comp1); n > 0 {
		e += m.comp1[phaseOf(bkt, n)]
	}
	if n := len(m.comp2); n > 0 {
		e += m.comp2[phaseOf(bkt, n)]
	}
	return e
}

func phaseOf(bkt int64, p int) int {
	return int(((bkt % int64(p)) + int64(p)) % int64(p))
}

// backfit estimates an additive seasonal decomposition value ≈ level +
// c1[phase1] + c2[phase2] by iterative back-fitting: hold one component fixed,
// re-estimate the other as the mean residual per phase, center it to zero mean,
// and repeat. A few passes converge for two well-separated periods. When p2 < 2
// it degenerates to a single-component (per-phase mean) fit.
func backfit(hist []float64, bkts []int64, p1, p2 int) (level float64, c1, c2 []float64) {
	c1 = make([]float64, p1)
	if p2 >= 2 {
		c2 = make([]float64, p2)
	}
	if len(hist) == 0 {
		return 0, c1, c2
	}
	var sum float64
	for _, v := range hist {
		sum += v
	}
	level = sum / float64(len(hist))

	reestimate := func(comp []float64, other []float64, po, pn int) {
		acc := make([]float64, pn)
		cnt := make([]int, pn)
		for j, v := range hist {
			r := v - level
			if len(other) > 0 {
				r -= other[phaseOf(bkts[j], po)]
			}
			ph := phaseOf(bkts[j], pn)
			acc[ph] += r
			cnt[ph]++
		}
		var mean float64
		for i := range comp {
			if cnt[i] > 0 {
				comp[i] = acc[i] / float64(cnt[i])
			}
			mean += comp[i]
		}
		mean /= float64(pn)
		for i := range comp {
			comp[i] -= mean // keep each component zero-mean; the level absorbs it
		}
	}
	passes := 1
	if p2 >= 2 {
		passes = 4
	}
	for iter := 0; iter < passes; iter++ {
		reestimate(c1, c2, p2, p1)
		if p2 >= 2 {
			reestimate(c2, c1, p1, p2)
		}
	}
	return level, c1, c2
}

// seasonalResidualVar is the variance of the residual after removing the
// additive fit (level + c1[+ c2]) — the yardstick the variance-gain test uses to
// decide whether a second period earns its own component.
func seasonalResidualVar(hist []float64, bkts []int64, p1, p2 int) float64 {
	if len(hist) == 0 {
		return 0
	}
	level, c1, c2 := backfit(hist, bkts, p1, p2)
	var s, s2 float64
	for j, v := range hist {
		e := level
		if len(c1) > 0 {
			e += c1[phaseOf(bkts[j], len(c1))]
		}
		if len(c2) > 0 {
			e += c2[phaseOf(bkts[j], len(c2))]
		}
		r := v - e
		s += r
		s2 += r * r
	}
	n := float64(len(hist))
	mean := s / n
	return s2/n - mean*mean
}

// candidatePeriods returns the detected periods that are usable as a cycle length
// (≥2 and ≤ ⅓ of history), de-duplicated, in the detector's prominence order.
func candidatePeriods(periods []int, histLen int) []int {
	seen := make(map[int]bool)
	out := make([]int, 0, len(periods))
	for _, p := range periods {
		if p >= 2 && p <= histLen/3 && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// Count is how many observations have been seen.
func (m *SeasonalModel) Count() int { return m.idx }

// Period returns the primary detected period (0 if none yet).
func (m *SeasonalModel) Period() int { return m.period }

// Period2 returns the second, independent seasonal period (0 if single-cycle).
func (m *SeasonalModel) Period2() int { return m.period2 }
