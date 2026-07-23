package detect

import (
	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/model"
	"github.com/urfan03/semeion/stats"
)

const distRefitEvery = 200

type DistributionModel struct {
	side     jobspec.Side
	prov     model.Provider
	window   int
	warmup   int
	history  []float64
	dist     model.Distribution
	sinceFit int
}

func NewDistributionModel(side jobspec.Side, prov model.Provider) *DistributionModel {
	if prov == nil {
		prov = model.NewGoProvider()
	}
	return &DistributionModel{side: side, prov: prov, window: defaultWindow, warmup: defaultWarmup}
}

func (m *DistributionModel) Observe(value float64) (prob, score, typical float64, dir core.Direction) {
	if !finite(value) {
		return 1, 0, 0, core.DirUp
	}
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

func (m *DistributionModel) Family() string { return m.dist.Family }

func (m *DistributionModel) Bounds(z float64) (lower, upper float64) {
	if len(m.history) == 0 {
		return 0, 0
	}
	med, mad := stats.MAD(m.history)
	scale := 1.4826 * mad
	if scale <= 0 {
		_, std := stats.MeanStd(m.history)
		scale = std
	}
	return med - z*scale, med + z*scale
}
