package detect

import (
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/model"
)

const (
	seasonalMinHistory  = 40
	seasonalRedetect    = 300
	seasonalPhaseWarmup = 4
	seasonalMaxHistory  = 6000
)

const multiMinGain = 0.10

type SeasonalModel struct {
	side   jobspec.Side
	prov   model.Provider
	global *Model
	span   time.Duration

	history []float64
	histBkt []int64
	idx     int

	period      int
	phases      []*Model
	sinceDetect int
	last        *Model

	period2      int
	level        float64
	comp1        []float64
	comp2        []float64
	resid        *Model
	lastExpected float64
}

func (m *SeasonalModel) multiActive() bool {
	return m.period2 >= 2 && len(m.comp1) == m.period && len(m.comp2) == m.period2 && m.resid != nil
}

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

func NewSeasonalModel(side jobspec.Side, prov model.Provider, span time.Duration) *SeasonalModel {
	if prov == nil {
		prov = model.NewGoProvider()
	}
	if span <= 0 {
		span = time.Minute
	}
	return &SeasonalModel{side: side, prov: prov, global: NewModel(side), span: span}
}

func (m *SeasonalModel) bucketIndex(t time.Time) int64 {
	return t.UnixNano() / int64(m.span)
}

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
		typical = exp
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

func (m *SeasonalModel) setupMulti(p1, p2 int) {
	if p1 == m.period && p2 == m.period2 {
		return
	}
	m.period, m.period2 = p1, p2
	m.phases = nil
	m.level, m.comp1, m.comp2 = backfit(m.history, m.histBkt, p1, p2)
	m.resid = NewModel(m.side)
	for j, v := range m.history {
		m.resid.Learn(v - m.expectedAt(m.histBkt[j]))
	}
}

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
			comp[i] -= mean
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

func (m *SeasonalModel) Count() int { return m.idx }

func (m *SeasonalModel) Period() int { return m.period }

func (m *SeasonalModel) Period2() int { return m.period2 }
