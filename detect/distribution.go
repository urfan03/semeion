package detect

import (
	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/model"
	"github.com/urfan03/semeion/stats"
)

const distRefitEvery = 200 // re-fit the distribution every N observations

// DistributionModel scores a value by its two-sided tail probability under a
// best-fit distribution (normal / lognormal / exponential / poisson), rather
// than a Gaussian z-score — accurate for skewed or count data. The distribution
// is re-fit periodically from the recent window.
type DistributionModel struct {
	side     jobspec.Side
	prov     model.Provider
	window   int
	warmup   int
	history  []float64
	dist     model.Distribution
	sinceFit int
}

// NewDistributionModel builds a distribution-based model.
func NewDistributionModel(side jobspec.Side, prov model.Provider) *DistributionModel {
	if prov == nil {
		prov = model.NewGoProvider()
	}
	return &DistributionModel{side: side, prov: prov, window: defaultWindow, warmup: defaultWarmup}
}

// Observe scores value under the fitted distribution, then folds it in.
func (m *DistributionModel) Observe(value float64) (prob, score, typical float64, dir core.Direction) {
	if len(m.history) < m.warmup {
		m.push(value)
		return 1, 0, value, core.DirUp
	}
	if m.dist.Family == "" || m.sinceFit >= distRefitEvery {
		m.dist = m.prov.FitDistribution(m.history)
		m.sinceFit = 0
	}
	m.sinceFit++

	typical = stats.Median(m.history)
	dir = core.DirUp
	if value < typical {
		dir = core.DirDown
	}

	prob = m.dist.Tail(value)
	// One-sided detectors ignore the "safe" direction.
	if (m.side == jobspec.SideHigh && dir == core.DirDown) ||
		(m.side == jobspec.SideLow && dir == core.DirUp) {
		prob = 1
	}
	score = scoreFromProbability(prob)
	m.push(value)
	return prob, score, typical, dir
}

func (m *DistributionModel) push(v float64) {
	m.history = append(m.history, v)
	if len(m.history) > m.window {
		m.history = m.history[len(m.history)-m.window:]
	}
}

// Family returns the currently fitted distribution family ("" until fit).
func (m *DistributionModel) Family() string { return m.dist.Family }
