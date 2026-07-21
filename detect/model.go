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
)

// Model is one time-series' streaming baseline. It keeps a bounded window of
// recent bucket values and scores each new value against a robust (median/MAD)
// estimate of that window. Not safe for concurrent use — the engine owns one
// Model per (detector, series).
type Model struct {
	side    jobspec.Side
	window  int
	warmup  int
	history []float64
}

// NewModel builds a model for a detector's side (both/high/low).
func NewModel(side jobspec.Side) *Model {
	return &Model{side: side, window: defaultWindow, warmup: defaultWarmup}
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
	return &Model{side: side, window: window, warmup: warmup}
}

// Score evaluates value against the current baseline WITHOUT updating it — the
// "peek" used by population analysis (score every member against the shared
// baseline, then fold them all in). During warm-up the score is 0.
func (m *Model) Score(value float64) (prob, score, typical float64, dir core.Direction) {
	if len(m.history) < m.warmup {
		return 1, 0, value, core.DirUp
	}

	med, mad := stats.MAD(m.history)
	typical = med
	z := stats.ModifiedZScore(value, med, mad)
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
	return prob, score, typical, dir
}

// Learn folds value into the baseline.
func (m *Model) Learn(value float64) { m.push(value) }

// Observe scores value against the learned baseline, then folds it in. It
// returns the tail probability, the 0..100 score, the typical (expected) value,
// and the deviation direction. During warm-up the score is 0.
func (m *Model) Observe(value float64) (prob, score, typical float64, dir core.Direction) {
	prob, score, typical, dir = m.Score(value)
	m.Learn(value)
	return prob, score, typical, dir
}

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
	Side    jobspec.Side `json:"side"`
	Window  int          `json:"window"`
	Warmup  int          `json:"warmup"`
	History []float64    `json:"history"`
}

// State returns a copy of the model's internal state.
func (m *Model) State() ModelState {
	return ModelState{
		Side:    m.side,
		Window:  m.window,
		Warmup:  m.warmup,
		History: append([]float64(nil), m.history...),
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
	return &Model{
		side:    s.Side,
		window:  s.Window,
		warmup:  s.Warmup,
		history: append([]float64(nil), s.History...),
	}
}
