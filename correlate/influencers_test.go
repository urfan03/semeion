package correlate

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
)

func TestRankInfluencers(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rec := func(min int, score float64, host string, share float64) core.Record {
		return core.Record{Time: t0.Add(time.Duration(min) * time.Minute), Score: score,
			Influencers: []core.Influencer{{Field: "host", Value: host, Score: share}}}
	}
	results := []core.BucketResult{
		{Records: []core.Record{rec(0, 90, "db-3", 1.0), rec(0, 40, "web-1", 1.0)}},
		{Records: []core.Record{rec(1, 80, "db-3", 1.0)}},
		{Records: []core.Record{rec(2, 30, "web-1", 1.0)}},
	}
	ranked := RankInfluencers(results, "")
	if len(ranked) != 2 {
		t.Fatalf("expected 2 influencers, got %d", len(ranked))
	}
	if ranked[0].Value != "db-3" {
		t.Fatalf("db-3 carried the most mass (90+80) and should rank first, got %q", ranked[0].Value)
	}
	if ranked[0].Records != 2 || ranked[0].Total != 170 || ranked[0].MaxScore != 90 {
		t.Fatalf("db-3 aggregation wrong: %+v", ranked[0])
	}

	only := RankInfluencers(results, "nonexistent")
	if len(only) != 0 {
		t.Fatalf("filtering to a missing field should yield nothing, got %d", len(only))
	}
}
