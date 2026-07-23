package detect

import (
	"math"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/stats"
)

const (
	// defaultWindow bounds the baseline memory (buckets). At a 5-minute span,
	// 1024 buckets ≈ 3.5 days — enough for a robust recent baseline without
	// unbounded growth. (Seasonality-aware baselines arrive in a later phase.)
	defaultWindow = 1024
	// defaultWarmup is how many buckets must be seen before scoring begins.
	defaultWarmup = 20
	// defaultMBWindow is the multi-bucket window: a sustained deviation over this
	// many buckets is flagged even when each bucket alone is only mildly unusual.
	defaultMBWindow = 12
)

// Model is one time-series' streaming baseline. It keeps a bounded window of
// recent bucket values and scores each new value against a robust (median/MAD)
// estimate of that window, plus a MULTI-BUCKET score over a short window of
// recent standardized residuals. Not safe for concurrent use — the engine owns
// one Model per (detector, series).
type Model struct {
	side      jobspec.Side
	window    int
	warmup    int
	history   []float64
	mbWindow  int
	recent    []float64 // recent signed standardized residuals (multi-bucket)
	lastMulti bool      // whether the last Observe was driven by multi-bucket

	// last-evaluated baseline centre + scale, for the typical-range bounds.
	lastTypical float64
	lastScale   float64
	// drift: consecutive recent buckets far from the baseline in one direction.
	driftRun  int
	driftSign int
}

// NewModel builds a model for a detector's side (both/high/low).
func NewModel(side jobspec.Side) *Model {
	return &Model{side: side, window: defaultWindow, warmup: defaultWarmup, mbWindow: defaultMBWindow}
}

// NewModelWarmup builds a model with explicit window/warm-up (used for the
// per-phase baselines of a seasonal model and per-slot time-of-day baselines,
// which see far fewer samples).
func NewModelWarmup(side jobspec.Side, window, warmup int) *Model {
	if window <= 0 {
		window = defaultWindow
	}
	if warmup < 0 {
		warmup = defaultWarmup
	}
	return &Model{side: side, window: window, warmup: warmup, mbWindow: defaultMBWindow}
}

// Score evaluates value against the current baseline WITHOUT updating it — the
// "peek" used by population analysis (score every member against the shared
// baseline, then fold them all in). During warm-up the score is 0.
func (m *Model) Score(value float64) (prob, score, typical float64, dir core.Direction) {
	prob, score, typical, _, dir = m.evaluate(value)
	return prob, score, typical, dir
}

// evaluate is Score plus the signed, side-adjusted standardized residual z
// (used by the multi-bucket accumulator).
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

	// One-sided detectors ignore deviations in the "safe" direction.
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

	// Two-sided for a both-sided detector (a spike OR a dip is anomalous), one-
	// sided for high/low. This matches the DistributionModel's convention so a
	// given extremeness scores the same whichever detector produced it.
	prob = stats.UpperTail(math.Abs(z))
	if m.side == jobspec.SideBoth {
		prob = math.Min(1, 2*prob)
	}
	score = scoreFromProbability(prob)
	// Warm-up confidence ramp: just after warm-up the baseline is thin, so damp
	// the score for the first few buckets to avoid early false positives.
	score *= m.warmupRamp()
	return prob, score, typical, z, dir
}

// baseline returns the expected value (typical) and the robust scale for the
// current window. When the window is materially TRENDING (a linear fit explains
// most of its variance), it scores against the fitted trend's residuals — so a
// steadily rising metric is judged by its deviation from the trend line, not
// flagged wholesale once it climbs above a lagging median. Otherwise it uses the
// robust median/MAD (with the flat-baseline scale floor).
func (m *Model) baseline() (typical, scale float64) {
	med, mad := stats.MAD(m.history)
	if n := len(m.history); n >= trendMinPoints {
		if slope, intercept, r2 := olsTrend(m.history); r2 >= trendR2 && consistentTrend(m.history, slope) {
			expected := intercept + slope*float64(n) // project to the new point
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
		// Flat baseline: >half the window equals the median so the robust spread
		// is zero. The full-window std would be wrong (it measures the very
		// outliers we want to flag), so use a data-relative floor instead.
		s = robustScaleFloor(m.history, med)
	}
	return med, s
}

// warmupRamp scales the score from ~0 up to 1 over the first warmupRamp buckets
// after warm-up, so a thin, freshly-warmed baseline can't fire at full strength.
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

// Bounds returns the typical range [typical − z·scale, typical + z·scale] from
// the last evaluation — the model_lower/upper band for charting and for judging
// how far outside "normal" a value sits.
func (m *Model) Bounds(z float64) (lower, upper float64) {
	return m.lastTypical - z*m.lastScale, m.lastTypical + z*m.lastScale
}

// consistentTrend guards the trend path against a STEP masquerading as a trend:
// a genuine linear trend rises in BOTH halves of the window (each half-slope
// agrees in sign with the whole and is a meaningful fraction of it), whereas a
// level shift is ~flat within each half with the jump between them. Without this
// a sustained level-shift anomaly (or an outage) would be projected as "expected"
// and silently masked.
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

// olsTrend fits y = intercept + slope·i by ordinary least squares and returns
// the fit plus its R² (fraction of variance explained).
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
	// trendR2: a window whose linear fit explains at least this fraction of
	// variance is treated as trending (score against the trend, not a flat mean).
	trendR2        = 0.5
	trendMinPoints = 8
	// warmupRampBuckets: how many buckets after warm-up the score ramps to full.
	warmupRampBuckets = 8
	// driftBuckets: a same-direction deviation sustained THIS many buckets is a
	// concept drift (a new normal), not an anomaly, so the baseline rebases. Set
	// far above the multi-bucket window so a genuine sustained outage still alerts
	// for a long time before being accepted.
	driftBuckets = 200
)

// constantRelFloor is the minimum detectable deviation on a constant continuous
// baseline, as a fraction of the level (2%): the scale of "normal" variation is
// zero, so we treat a 2%-of-level move as the unit deviation.
const constantRelFloor = 0.02

// robustScaleFloor returns a scale for a (near-)constant baseline where the
// robust spread (MAD) is zero. It deliberately does NOT use the window's
// standard deviation, which would be inflated by the outliers we are trying to
// detect. For count data it uses a Poisson √mean floor; for continuous data a
// small fraction of the level, so the same relative jump scores the same at any
// magnitude.
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
	return 1e-9 // a constant-zero continuous baseline: any move is significant
}

// isIntegerSeries reports whether every sampled value is a whole number (count
// data). It samples up to 64 values to stay cheap on a large window.
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

// Learn folds value into the baseline.
func (m *Model) Learn(value float64) { m.push(value) }

// Observe scores value against the learned baseline, folds it in, and updates
// the multi-bucket accumulator — a sustained deviation over the recent window
// elevates the score even when each bucket alone is only mildly unusual.
func (m *Model) Observe(value float64) (prob, score, typical float64, dir core.Direction) {
	var z float64
	prob, score, typical, z, dir = m.evaluate(value)

	m.lastMulti = false
	if m.mbWindow > 1 && len(m.history) >= m.warmup { // not during warm-up
		m.recent = append(m.recent, z)
		if len(m.recent) > m.mbWindow {
			m.recent = m.recent[len(m.recent)-m.mbWindow:]
		}
		if len(m.recent) == m.mbWindow {
			// MEDIAN residual, standardized to unit variance: a genuine SUSTAINED
			// shift (most of the window elevated) flags; a single spike doesn't move
			// the median, so one outlier can't masquerade as M sustained anomalies.
			// The sample median of M unit-normal residuals has std √(π/2M), so the
			// standardizing factor is √(2M/π) — NOT √M (which leaves the statistic
			// over-dispersed by √(π/2)≈1.25 and inflates the false-positive rate).
			mbZ := stats.Median(m.recent) * math.Sqrt(2*float64(m.mbWindow)/math.Pi)
			mbProb := stats.UpperTail(math.Abs(mbZ))
			if m.side == jobspec.SideBoth {
				mbProb = math.Min(1, 2*mbProb)
			}
			if mbScore := scoreFromProbability(mbProb); mbScore > score {
				prob, score, m.lastMulti = mbProb, mbScore, true
			}
		}
	}
	m.trackDrift(z)
	m.Learn(value)
	return prob, score, typical, dir
}

// trackDrift watches for a CONCEPT DRIFT: a sustained level shift, distinct from
// a transient anomaly. It counts consecutive buckets that sit far (|z| ≥ 2) from
// the baseline in the SAME direction; once that run reaches driftBuckets — far
// longer than the multi-bucket window, so a real outage isn't quietly accepted —
// the pre-shift history is dropped so the baseline re-centres on the new normal.
// Without this, a permanent level change would flag every bucket forever.
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
		// Rebase: keep only the most recent driftBuckets values (the new regime).
		if len(m.history) > driftBuckets {
			m.history = append([]float64(nil), m.history[len(m.history)-driftBuckets:]...)
		}
		m.recent = nil
		m.driftRun, m.driftSign = 0, 0
	}
}

// LastMulti reports whether the most recent Observe was driven by the
// multi-bucket (sustained) signal rather than the single bucket.
func (m *Model) LastMulti() bool { return m.lastMulti }

// Count is how many observations the model has learned (for warm-up checks).
func (m *Model) Count() int { return len(m.history) }

func (m *Model) push(v float64) {
	m.history = append(m.history, v)
	if len(m.history) > m.window {
		m.history = m.history[len(m.history)-m.window:]
	}
}

// ModelState is the serialisable snapshot of a Model, used to persist and
// restore a running detector so its learned baseline survives restarts.
type ModelState struct {
	Side     jobspec.Side `json:"side"`
	Window   int          `json:"window"`
	Warmup   int          `json:"warmup"`
	History  []float64    `json:"history"`
	MBWindow int          `json:"mb_window,omitempty"`
	Recent   []float64    `json:"recent,omitempty"`
}

// State returns a copy of the model's internal state.
func (m *Model) State() ModelState {
	return ModelState{
		Side:     m.side,
		Window:   m.window,
		Warmup:   m.warmup,
		History:  append([]float64(nil), m.history...),
		MBWindow: m.mbWindow,
		Recent:   append([]float64(nil), m.recent...),
	}
}

// ModelFromState rebuilds a Model from a persisted state (defaults fill zeros).
func ModelFromState(s ModelState) *Model {
	if s.Window <= 0 {
		s.Window = defaultWindow
	}
	if s.Warmup < 0 {
		s.Warmup = defaultWarmup
	}
	if s.MBWindow <= 0 {
		s.MBWindow = defaultMBWindow
	}
	return &Model{
		side:     s.Side,
		window:   s.Window,
		warmup:   s.Warmup,
		history:  append([]float64(nil), s.History...),
		mbWindow: s.MBWindow,
		recent:   append([]float64(nil), s.Recent...),
	}
}
