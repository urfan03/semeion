package detect

import (
	"time"

	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/model"
)

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

	Period2 int         `json:"period2,omitempty"`
	Level   float64     `json:"level,omitempty"`
	Comp1   []float64   `json:"comp1,omitempty"`
	Comp2   []float64   `json:"comp2,omitempty"`
	Resid   *ModelState `json:"resid,omitempty"`
}

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
		Period2:     m.period2,
		Level:       m.level,
		Comp1:       append([]float64(nil), m.comp1...),
		Comp2:       append([]float64(nil), m.comp2...),
	}
	for _, ph := range m.phases {
		s.Phases = append(s.Phases, ph.State())
	}
	if m.resid != nil {
		rs := m.resid.State()
		s.Resid = &rs
	}
	return s
}

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
		period2:     s.Period2,
		level:       s.Level,
		comp1:       append([]float64(nil), s.Comp1...),
		comp2:       append([]float64(nil), s.Comp2...),
	}
	for _, ps := range s.Phases {
		m.phases = append(m.phases, ModelFromState(ps))
	}
	if s.Resid != nil {
		m.resid = ModelFromState(*s.Resid)
	}
	return m
}

type DistributionState struct {
	Side     jobspec.Side       `json:"side"`
	Window   int                `json:"window"`
	Warmup   int                `json:"warmup"`
	History  []float64          `json:"history,omitempty"`
	Dist     model.Distribution `json:"dist"`
	SinceFit int                `json:"since_fit"`
}

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

type MultivariateState struct {
	K       int         `json:"k"`
	Window  int         `json:"window"`
	Warmup  int         `json:"warmup"`
	History [][]float64 `json:"history,omitempty"`
}

func (m *MultivariateModel) State() MultivariateState {
	hist := make([][]float64, len(m.history))
	for i, row := range m.history {
		hist[i] = append([]float64(nil), row...)
	}
	return MultivariateState{K: m.k, Window: m.window, Warmup: m.warmup, History: hist}
}

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
