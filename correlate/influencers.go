package correlate

import (
	"sort"

	"github.com/urfan03/semeion/core"
)

// Influencer-level aggregation is the "who is responsible" view across a whole
// analysis window (Elastic ML's Influencers tab): instead of per-bucket records,
// it rolls every record's dimension attributions up into a ranked list of the
// entities (host, user, service, country, …) that carried the most anomalous
// mass — so an operator sees "host db-3 accounts for most of today's anomalies"
// at a glance.

// InfluencerScore is one aggregated (field, value) attribution over a set of
// results.
type InfluencerScore struct {
	Field    string  `json:"field"`
	Value    string  `json:"value"`
	Records  int     `json:"records"`   // records this value was attributed to
	MaxScore float64 `json:"max_score"` // the single most anomalous record it touched
	Total    float64 `json:"total"`     // summed contribution (record score × attribution share)
}

// RankInfluencers aggregates every record's influencers across the results and
// ranks them by total contribution. A record's contribution to an influencer is
// its anomaly score weighted by that influencer's attribution share (falling back
// to the full score when no share was computed). Optionally filter to a single
// field (e.g. only "host") by passing a non-empty field.
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
