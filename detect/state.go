package detect

import (
	"time"

	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/model"
)

// This file adds snapshot/restore for the non-plain model families (seasonal,
// distribution, multivariate) so a running detector survives a restart with ALL
// of its learned state, not just the plain z-score path. Every field that
// carries learned state is persisted; derived quantities (mean/cov, fitted
// distribution) are recomputed on the next Observe.

// ── SeasonalModel ────────────────────────────────────────────────────────────

// SeasonalState persists a SeasonalModel: the global baseline (used before a
// period is found), the per-phase baselines (after), and the history/counters
// needed for future re-detection.
type SeasonalState struct {
	Side        jobspec.Side  `json:"side"`
	Span        time.Duration `json:"span"`
	History     []float64     `json:"history,omitempty"`
	HistBkt     []int64       `json:"hist_bkt,omitempty"`
	Idx         int           `json:"idx"`
	Period      int           `json:"period"`
	SinceDetect int           `json:"since_detect"`
	Global      ModelState    `json:"global"`
	Phases      []ModelState  `json:"phases,omitempty"`
}

// State returns a copy of the seasonal model's learned state.
func (m *SeasonalModel) State() SeasonalState {
	s := SeasonalState{
		Side:        m.side,
		Span:        m.span,
		History:     append([]float64(nil), m.history...),
		HistBkt:     append([]int64(nil), m.histBkt...),
		Idx:         m.idx,
		Period:      m.period,
		SinceDetect: m.sinceDetect,
		Global:      m.global.State(),
	}
	for _, ph := range m.phases {
		s.Phases = append(s.Phases, ph.State())
	}
	return s
}

// SeasonalFromState rebuilds a SeasonalModel from a persisted state.
func SeasonalFromState(s SeasonalState, prov model.Provider) *SeasonalModel {
	if prov == nil {
		prov = model.NewGoProvider()
	}
	span := s.Span
	if span <= 0 {
		span = time.Minute
	}
	m := &SeasonalModel{
		side:        s.Side,
		prov:        prov,
		span:        span,
		global:      ModelFromState(s.Global),
		history:     append([]float64(nil), s.History...),
		histBkt:     append([]int64(nil), s.HistBkt...),
		idx:         s.Idx,
		period:      s.Period,
		sinceDetect: s.SinceDetect,
	}
	for _, ps := range s.Phases {
		m.phases = append(m.phases, ModelFromState(ps))
	}
	return m
}

// ── DistributionModel ────────────────────────────────────────────────────────

// DistributionState persists a DistributionModel: its window, the fitted
// distribution, and the recent history the next refit uses.
type DistributionState struct {
	Side     jobspec.Side       `json:"side"`
	Window   int                `json:"window"`
	Warmup   int                `json:"warmup"`
	History  []float64          `json:"history,omitempty"`
	Dist     model.Distribution `json:"dist"`
	SinceFit int                `json:"since_fit"`
}

// State returns a copy of the distribution model's learned state.
func (m *DistributionModel) State() DistributionState {
	return DistributionState{
		Side:     m.side,
		Window:   m.window,
		Warmup:   m.warmup,
		History:  append([]float64(nil), m.history...),
		Dist:     m.dist,
		SinceFit: m.sinceFit,
	}
}

// DistributionFromState rebuilds a DistributionModel from a persisted state.
func DistributionFromState(s DistributionState, prov model.Provider) *DistributionModel {
	if prov == nil {
		prov = model.NewGoProvider()
	}
	if s.Window <= 0 {
		s.Window = defaultWindow
	}
	if s.Warmup < 0 {
		s.Warmup = defaultWarmup
	}
	return &DistributionModel{
		side:     s.Side,
		prov:     prov,
		window:   s.Window,
		warmup:   s.Warmup,
		history:  append([]float64(nil), s.History...),
		dist:     s.Dist,
		sinceFit: s.SinceFit,
	}
}

// ── MultivariateModel ────────────────────────────────────────────────────────

// MultivariateState persists a MultivariateModel: the vectors it has learned
// (mean/covariance are recomputed each Observe, so only history is needed).
type MultivariateState struct {
	K       int         `json:"k"`
	Window  int         `json:"window"`
	Warmup  int         `json:"warmup"`
	History [][]float64 `json:"history,omitempty"`
}

// State returns a copy of the multivariate model's learned state.
func (m *MultivariateModel) State() MultivariateState {
	hist := make([][]float64, len(m.history))
	for i, row := range m.history {
		hist[i] = append([]float64(nil), row...)
	}
	return MultivariateState{K: m.k, Window: m.window, Warmup: m.warmup, History: hist}
}

// MultivariateFromState rebuilds a MultivariateModel from a persisted state.
func MultivariateFromState(s MultivariateState) *MultivariateModel {
	if s.Window <= 0 {
		s.Window = defaultWindow
	}
	if s.Warmup < 0 {
		s.Warmup = defaultWarmup
	}
	hist := make([][]float64, len(s.History))
	for i, row := range s.History {
		hist[i] = append([]float64(nil), row...)
	}
	return &MultivariateModel{k: s.K, window: s.Window, warmup: s.Warmup, history: hist}
}
