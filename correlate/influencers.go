package correlate

import (
	"sort"

	"github.com/urfan03/semeion/core"
)

type InfluencerScore struct {
	Field    string  `json:"field"`
	Value    string  `json:"value"`
	Records  int     `json:"records"`
	MaxScore float64 `json:"max_score"`
	Total    float64 `json:"total"`
}

func RankInfluencers(results []core.BucketResult, field string) []InfluencerScore {
	type key struct{ f, v string }
	agg := map[key]*InfluencerScore{}
	for _, br := range results {
		for _, r := range br.Records {
			for _, in := range r.Influencers {
				if in.Value == "" || (field != "" && in.Field != field) {
					continue
				}
				share := in.Score
				if share <= 0 {
					share = 1
				}
				k := key{in.Field, in.Value}
				s := agg[k]
				if s == nil {
					s = &InfluencerScore{Field: in.Field, Value: in.Value}
					agg[k] = s
				}
				s.Records++
				s.Total += r.Score * share
				if r.Score > s.MaxScore {
					s.MaxScore = r.Score
				}
			}
		}
	}
	out := make([]InfluencerScore, 0, len(agg))
	for _, s := range agg {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		if out[i].MaxScore != out[j].MaxScore {
			return out[i].MaxScore > out[j].MaxScore
		}
		return out[i].Field+out[i].Value < out[j].Field+out[j].Value
	})
	return out
}
