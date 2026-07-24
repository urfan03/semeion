package detect

import (
	"math"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/stats"
)

const (
	defaultWindow = 1024

	defaultWarmup = 20

	defaultMBWindow = 12
)

type Model struct {
	side      jobspec.Side
	window    int
	warmup    int
	history   []float64
	mbWindow  int
	recent    []float64
	lastMulti bool

	lastTypical float64
	lastScale   float64
	lastSingle  float64
	lastMB      float64

	driftRun  int
	driftSign int
}

func NewModel(side jobspec.Side) *Model {
	return &Model{side: side, window: defaultWindow, warmup: defaultWarmup, mbWindow: defaultMBWindow}
}

func NewModelWarmup(side jobspec.Side, window, warmup int) *Model {
	if window <= 0 {
		window = defaultWindow
	}
	if warmup < 0 {
		warmup = defaultWarmup
	}
	return &Model{side: side, window: window, warmup: warmup, mbWindow: defaultMBWindow}
}

func (m *Model) Score(value float64) (prob, score, typical float64, dir core.Direction) {
	if !finite(value) {
		return 1, 0, m.lastTypical, core.DirUp
	}
	prob, score, typical, _, dir = m.evaluate(value)
	return prob, score, typical, dir
}

func (m *Model) evaluate(value float64) (prob, score, typical, z float64, dir core.Direction) {
	if len(m.history) < m.warmup {
		return 1, 0, value, 0, core.DirUp
	}

	typical, scale := m.baseline()
	m.lastTypical, m.lastScale = typical, scale
	z = (value - typical) / scale

	dir = core.DirUp
	if z < 0 {
		dir = core.DirDown
	}

	switch m.side {
	case jobspec.SideHigh:
		if z < 0 {
			z = 0
		}
	case jobspec.SideLow:
		if z > 0 {
			z = 0
		}
	}

	prob = stats.UpperTail(math.Abs(z))
	if m.side == jobspec.SideBoth {
		prob = math.Min(1, 2*prob)
	}
	score = scoreFromProbability(prob)

	score *= m.warmupRamp()
	return prob, score, typical, z, dir
}

func (m *Model) baseline() (typical, scale float64) {
	med, mad := stats.MAD(m.history)
	if n := len(m.history); n >= trendMinPoints {
		if slope, intercept, r2 := olsTrend(m.history); r2 >= trendR2 && consistentTrend(m.history, slope) {
			expected := intercept + slope*float64(n)
			resid := make([]float64, n)
			for i, v := range m.history {
				resid[i] = v - (intercept + slope*float64(i))
			}
			_, rmad := stats.MAD(resid)
			s := 1.4826 * rmad
			if s <= 0 {
				s = robustScaleFloor(m.history, med)
			}
			return expected, s
		}
	}
	s := 1.4826 * mad
	if s <= 0 {

		s = robustScaleFloor(m.history, med)
	}
	return med, s
}

func (m *Model) warmupRamp() float64 {
	extra := len(m.history) - m.warmup
	if extra >= warmupRampBuckets {
		return 1
	}
	if extra < 0 {
		return 0
	}
	return float64(extra+1) / float64(warmupRampBuckets+1)
}

func (m *Model) Bounds(z float64) (lower, upper float64) {
	return m.lastTypical - z*m.lastScale, m.lastTypical + z*m.lastScale
}

func consistentTrend(y []float64, fullSlope float64) bool {
	if fullSlope == 0 || len(y) < 2*trendMinPoints {
		return false
	}
	h := len(y) / 2
	s1, _, _ := olsTrend(y[:h])
	s2, _, _ := olsTrend(y[h:])
	min := 0.3 * math.Abs(fullSlope)
	return sameSign(s1, fullSlope) && sameSign(s2, fullSlope) &&
		math.Abs(s1) >= min && math.Abs(s2) >= min
}

func sameSign(a, b float64) bool { return (a > 0 && b > 0) || (a < 0 && b < 0) }

func olsTrend(y []float64) (slope, intercept, r2 float64) {
	n := float64(len(y))
	var sx, sy, sxx, sxy float64
	for i, v := range y {
		x := float64(i)
		sx += x
		sy += v
		sxx += x * x
		sxy += x * v
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0, sy / n, 0
	}
	slope = (n*sxy - sx*sy) / den
	intercept = (sy - slope*sx) / n
	meanY := sy / n
	var ssRes, ssTot float64
	for i, v := range y {
		fit := intercept + slope*float64(i)
		ssRes += (v - fit) * (v - fit)
		ssTot += (v - meanY) * (v - meanY)
	}
	if ssTot == 0 {
		return slope, intercept, 0
	}
	return slope, intercept, 1 - ssRes/ssTot
}

const (
	trendR2        = 0.5
	trendMinPoints = 8

	warmupRampBuckets = 8

	driftBuckets = 200
)

const constantRelFloor = 0.02

func robustScaleFloor(history []float64, center float64) float64 {
	if isIntegerSeries(history) {
		s := math.Sqrt(math.Abs(center))
		if s < 1 {
			s = 1
		}
		return s
	}
	if s := math.Abs(center) * constantRelFloor; s > 1e-9 {
		return s
	}
	return 1e-9
}

func isIntegerSeries(history []float64) bool {
	if len(history) == 0 {
		return false
	}
	step := 1
	if len(history) > 64 {
		step = len(history) / 64
	}
	for i := 0; i < len(history); i += step {
		if history[i] != math.Trunc(history[i]) {
			return false
		}
	}
	return true
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

func (m *Model) Learn(value float64) {
	if !finite(value) {
		return
	}
	m.push(value)
}

func (m *Model) Observe(value float64) (prob, score, typical float64, dir core.Direction) {
	if !finite(value) {
		return 1, 0, m.lastTypical, core.DirUp
	}
	var z float64
	prob, score, typical, z, dir = m.evaluate(value)

	m.lastMulti = false
	m.lastSingle, m.lastMB = score, score
	if m.mbWindow > 1 && len(m.history) >= m.warmup {
		m.recent = append(m.recent, z)
		if len(m.recent) > m.mbWindow {
			m.recent = m.recent[len(m.recent)-m.mbWindow:]
		}
		if len(m.recent) == m.mbWindow {

			mbZ := stats.Median(m.recent) * math.Sqrt(2*float64(m.mbWindow)/math.Pi)
			mbProb := stats.UpperTail(math.Abs(mbZ))
			if m.side == jobspec.SideBoth {
				mbProb = math.Min(1, 2*mbProb)
			}
			mbScore := scoreFromProbability(mbProb)
			m.lastMB = mbScore
			if mbScore > score {
				prob, score, m.lastMulti = mbProb, mbScore, true
			}
		}
	}
	m.trackDrift(z)
	m.Learn(value)
	return prob, score, typical, dir
}

func (m *Model) trackDrift(z float64) {
	const driftThresh = 2.0
	if len(m.history) < m.warmup {
		return
	}
	sign := 0
	if z >= driftThresh {
		sign = 1
	} else if z <= -driftThresh {
		sign = -1
	}
	if sign == 0 || sign != m.driftSign {
		m.driftRun, m.driftSign = 0, sign
	}
	if sign != 0 {
		m.driftRun++
	}
	if m.driftRun >= driftBuckets {

		if len(m.history) > driftBuckets {
			m.history = append([]float64(nil), m.history[len(m.history)-driftBuckets:]...)
		}
		m.recent = nil
		m.driftRun, m.driftSign = 0, 0
	}
}

func (m *Model) LastMulti() bool { return m.lastMulti }

func (m *Model) MultiBucketImpact() float64 {
	if m.lastMB <= m.lastSingle || m.lastMB <= 0 {
		return 0
	}
	impact := 5 * (m.lastMB - m.lastSingle) / m.lastMB
	if impact > 5 {
		return 5
	}
	return impact
}

func (m *Model) Count() int { return len(m.history) }

func (m *Model) push(v float64) {
	m.history = append(m.history, v)
	if len(m.history) > m.window {
		m.history = m.history[len(m.history)-m.window:]
	}
}

type ModelState struct {
	Side      jobspec.Side `json:"side"`
	Window    int          `json:"window"`
	Warmup    int          `json:"warmup"`
	History   []float64    `json:"history"`
	MBWindow  int          `json:"mb_window,omitempty"`
	Recent    []float64    `json:"recent,omitempty"`
	DriftRun  int          `json:"drift_run,omitempty"`
	DriftSign int          `json:"drift_sign,omitempty"`
}

func (m *Model) State() ModelState {
	return ModelState{
		Side:      m.side,
		Window:    m.window,
		Warmup:    m.warmup,
		History:   append([]float64(nil), m.history...),
		MBWindow:  m.mbWindow,
		Recent:    append([]float64(nil), m.recent...),
		DriftRun:  m.driftRun,
		DriftSign: m.driftSign,
	}
}

func ModelFromState(s ModelState) *Model {
	if s.Window <= 0 {
		s.Window = defaultWindow
	}
	if s.Warmup <= 0 {
		s.Warmup = defaultWarmup
	}
	if s.MBWindow <= 0 {
		s.MBWindow = defaultMBWindow
	}
	return &Model{
		side:      s.Side,
		window:    s.Window,
		warmup:    s.Warmup,
		history:   append([]float64(nil), s.History...),
		mbWindow:  s.MBWindow,
		recent:    append([]float64(nil), s.Recent...),
		driftRun:  s.DriftRun,
		driftSign: s.DriftSign,
	}
}
