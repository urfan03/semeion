package correlate

import (
	"sort"

	"github.com/urfan03/semeion/stats"
)

type LeadLagRank struct {
	Label       string  `json:"label"`
	Lag         int     `json:"lag"`
	Corr        float64 `json:"corr"`
	Improvement float64 `json:"improvement"`
	Leads       bool    `json:"leads"`
}

func OrderByCausality(target []float64, candidates map[string][]float64, maxLag, grangerOrder int) []LeadLagRank {
	ranks := make([]LeadLagRank, 0, len(candidates))
	for label, series := range candidates {
		lag, corr := stats.LeadLag(series, target, maxLag)
		improve, _ := stats.Granger(series, target, grangerOrder)
		ranks = append(ranks, LeadLagRank{
			Label:       label,
			Lag:         lag,
			Corr:        corr,
			Improvement: improve,
			Leads:       lag >= 0 && absF(corr) >= 0.3,
		})
	}

	sort.SliceStable(ranks, func(i, j int) bool {
		if ranks[i].Leads != ranks[j].Leads {
			return ranks[i].Leads
		}
		if ranks[i].Improvement != ranks[j].Improvement {
			return ranks[i].Improvement > ranks[j].Improvement
		}
		if absF(ranks[i].Corr) != absF(ranks[j].Corr) {
			return absF(ranks[i].Corr) > absF(ranks[j].Corr)
		}
		return ranks[i].Label < ranks[j].Label
	})
	return ranks
}

func absF(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
