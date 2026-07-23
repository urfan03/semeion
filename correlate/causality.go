package correlate

import (
	"sort"

	"github.com/urfan03/semeion/stats"
)

// Data-driven root-cause ordering. When several candidate metrics all deviate
// during an incident, the one whose series MOVED FIRST and whose past best
// predicts the target's future is the more likely driver. This complements the
// event-based ranking in Correlate (precedence, topology, deliberate changes)
// with a continuous-signal test the caller runs over the candidates' raw series.

// LeadLagRank scores one candidate series against the target.
type LeadLagRank struct {
	Label       string  `json:"label"`
	Lag         int     `json:"lag"`         // positive = candidate leads the target (moved earlier)
	Corr        float64 `json:"corr"`        // peak cross-correlation at that lag
	Improvement float64 `json:"improvement"` // Granger: fraction of target variance the candidate's past explains
	Leads       bool    `json:"leads"`       // lag >= 0 and correlated
}

// OrderByCausality ranks candidate series by how strongly they causally lead the
// target series: candidates that move earlier (lag ≥ 0) and improve the target's
// prediction (Granger) come first. maxLag bounds the cross-correlation search;
// grangerOrder is the autoregressive order of the Granger test (e.g. 2–4).
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
	// Rank: leaders first, then by Granger improvement, then by |correlation|.
	sort.Slice(ranks, func(i, j int) bool {
		if ranks[i].Leads != ranks[j].Leads {
			return ranks[i].Leads
		}
		if ranks[i].Improvement != ranks[j].Improvement {
			return ranks[i].Improvement > ranks[j].Improvement
		}
		return absF(ranks[i].Corr) > absF(ranks[j].Corr)
	})
	return ranks
}

func absF(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
