package engine

import (
	"math"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/stats"
)

func Ensemble(results []core.BucketResult) []core.BucketResult {
	out := make([]core.BucketResult, 0, len(results))
	for _, br := range results {
		bySeries := map[string][]core.Record{}
		var order []string
		for _, r := range br.Records {
			if _, ok := bySeries[r.Series]; !ok {
				order = append(order, r.Series)
			}
			bySeries[r.Series] = append(bySeries[r.Series], r)
		}
		nb := core.BucketResult{Time: br.Time}
		for _, sk := range order {
			recs := bySeries[sk]
			stat := 0.0
			detectors := make([]core.Influencer, 0, len(recs))
			for _, r := range recs {
				p := r.Probability
				if p <= 0 {
					p = probFromScore(r.Score)
				}
				if p < 1e-12 {
					p = 1e-12
				}
				if p > 1 {
					p = 1
				}
				stat += -2 * math.Log(p)
				detectors = append(detectors, core.Influencer{Field: "detector", Value: r.Detector, Score: r.Score / 100})
			}
			combined := stats.ChiSquareTail(stat, 2*len(recs))
			score := ensembleScore(combined)
			nb.Records = append(nb.Records, core.Record{
				Time: br.Time, Detector: "ensemble", Series: sk,
				Probability: combined, Score: score, Direction: core.DirUp,
				Kind: "ensemble", Influencers: detectors,
			})
			if score > nb.Score {
				nb.Score = score
			}
		}
		out = append(out, nb)
	}
	return out
}

func ensembleScore(p float64) float64 {
	if p <= 0 {
		return 100
	}
	s := -math.Log10(p) / 12 * 100
	if s > 100 {
		return 100
	}
	if s < 0 {
		return 0
	}
	return s
}
