package conformal

import (
	"math"
	"sort"
)

type Calibrator struct {
	cal   []float64
	alpha float64
}

func New(calibration []float64, alpha float64) *Calibrator {
	return NewTrimmed(calibration, alpha, 0)
}

func NewTrimmed(calibration []float64, alpha, trim float64) *Calibrator {
	clean := make([]float64, 0, len(calibration))
	for _, v := range calibration {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			clean = append(clean, v)
		}
	}
	sort.Float64s(clean)
	if alpha <= 0 || alpha >= 1 {
		alpha = 0.01
	}
	if trim > 0 && trim < 0.5 {
		keep := len(clean) - int(float64(len(clean))*trim)
		if keep >= MinCalibration(alpha) && keep > 0 {
			clean = clean[:keep]
		}
	}
	return &Calibrator{cal: clean, alpha: alpha}
}

func (c *Calibrator) Size() int { return len(c.cal) }

func (c *Calibrator) Alpha() float64 { return c.alpha }

func (c *Calibrator) P(score float64) float64 {
	n := len(c.cal)
	if n == 0 || math.IsNaN(score) {
		return 1
	}
	ge := n - sort.SearchFloat64s(c.cal, score)
	return float64(1+ge) / float64(n+1)
}

func (c *Calibrator) Alarm(score float64) bool { return c.P(score) <= c.alpha }

func (c *Calibrator) Threshold() float64 {
	n := len(c.cal)
	if n == 0 {
		return math.Inf(1)
	}
	k := int(math.Floor(c.alpha*float64(n+1) - 1))
	if k < 0 {
		return math.Inf(1)
	}
	if k >= n {
		return math.Inf(-1)
	}
	return c.cal[n-k-1]
}

func Guarantee(alpha float64, n int) float64 {
	if n < 1 {
		return 1
	}
	k := math.Floor(alpha * float64(n+1))
	if k < 0 {
		k = 0
	}
	return k / float64(n+1)
}

func MinCalibration(alpha float64) int {
	if alpha <= 0 || alpha >= 1 {
		return 0
	}
	return int(math.Ceil(1/alpha)) - 1
}

type Mondrian struct {
	period int
	slots  []*Calibrator
	alpha  float64
}

func NewMondrian(calibration []float64, offset, period int, alpha float64) *Mondrian {
	if period < 1 {
		period = 1
	}
	buckets := make([][]float64, period)
	for i, v := range calibration {
		s := ((i+offset)%period + period) % period
		buckets[s] = append(buckets[s], v)
	}
	m := &Mondrian{period: period, alpha: alpha, slots: make([]*Calibrator, period)}
	for i := range buckets {
		m.slots[i] = New(buckets[i], alpha)
	}
	return m
}

func (m *Mondrian) Period() int { return m.period }

func (m *Mondrian) slot(index int) *Calibrator {
	s := ((index)%m.period + m.period) % m.period
	return m.slots[s]
}

func (m *Mondrian) P(index int, score float64) float64 {
	c := m.slot(index)
	if c.Size() == 0 {
		return 1
	}
	return c.P(score)
}

func (m *Mondrian) Alarm(index int, score float64) bool {
	return m.P(index, score) <= m.alpha
}

func (m *Mondrian) Smallest() int {
	best := math.MaxInt
	for _, c := range m.slots {
		if c.Size() < best {
			best = c.Size()
		}
	}
	if best == math.MaxInt {
		return 0
	}
	return best
}

type StreamOptions struct {
	Alpha       float64
	Calibration int
	Period      int
	Slide       bool
}

func (o StreamOptions) resolve(n int) StreamOptions {
	if o.Alpha <= 0 || o.Alpha >= 1 {
		o.Alpha = 0.01
	}
	if o.Calibration <= 0 {
		o.Calibration = n / 4
	}
	if o.Calibration < MinCalibration(o.Alpha) {
		o.Calibration = MinCalibration(o.Alpha)
	}
	if o.Period < 1 {
		o.Period = 1
	}
	return o
}

func Probabilities(scores []float64, opt StreamOptions) []float64 {
	n := len(scores)
	opt = opt.resolve(n)
	out := make([]float64, n)
	for i := range out {
		out[i] = 1
	}
	if n <= opt.Calibration {
		return out
	}
	if opt.Period > 1 {
		m := NewMondrian(scores[:opt.Calibration], 0, opt.Period, opt.Alpha)
		for i := opt.Calibration; i < n; i++ {
			out[i] = m.P(i, scores[i])
		}
		return out
	}
	c := New(scores[:opt.Calibration], opt.Alpha)
	for i := opt.Calibration; i < n; i++ {
		out[i] = c.P(scores[i])
		if opt.Slide {
			c = New(scores[max(0, i-opt.Calibration+1):i+1], opt.Alpha)
		}
	}
	return out
}

func Scores(scores []float64, opt StreamOptions) []float64 {
	p := Probabilities(scores, opt)
	out := make([]float64, len(p))
	for i, v := range p {
		if v <= 0 {
			v = 1e-300
		}
		out[i] = -math.Log10(v)
	}
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
