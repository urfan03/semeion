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

	med, mad := stats.MAD(m.history)
	typical = med
	z = stats.ModifiedZScore(value, med, mad)
	if mad == 0 {
		// Flat baseline (half the window identical): fall back to mean/std, and
		// when that is also zero (a *perfectly* constant baseline, common for
		// integer counts), use a Poisson-style scale floor so a departure from
		// the constant norm is still caught instead of scoring zero.
		mean, std := stats.MeanStd(m.history)
		typical = mean
		if std > 0 {
			z = (value - mean) / std
		} else {
			scale := math.Sqrt(math.Abs(mean))
			if scale < 1 {
				scale = 1
			}
			z = (value - mean) / scale
		}
	}

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

	prob = stats.UpperTail(math.Abs(z))
	score = scoreFromProbability(prob)
	return prob, score, typical, z, dir
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
			// MEDIAN residual amplified by √M: a genuine SUSTAINED shift (most of
			// the window elevated) flags; a single spike doesn't move the median,
			// so one outlier can't masquerade as M sustained anomalies.
			mbZ := stats.Median(m.recent) * math.Sqrt(float64(m.mbWindow))
			mbProb := stats.UpperTail(math.Abs(mbZ))
			if mbScore := scoreFromProbability(mbProb); mbScore > score {
				prob, score, m.lastMulti = mbProb, mbScore, true
			}
		}
	}
	m.Learn(value)
	return prob, score, typical, dir
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
