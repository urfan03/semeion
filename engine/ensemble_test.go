package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
)

func TestEnsembleBoostsAgreement(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	results := []core.BucketResult{{
		Time: t0,
		Records: []core.Record{
			{Time: t0, Detector: "mean(latency)", Series: "host=a", Score: 60},
			{Time: t0, Detector: "count", Series: "host=a", Score: 62},
			{Time: t0, Detector: "mean(latency)", Series: "host=b", Score: 61},
		},
	}}
	ens := Ensemble(results)
	if len(ens) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(ens))
	}
	var a, b *core.Record
	for i := range ens[0].Records {
		switch ens[0].Records[i].Series {
		case "host=a":
			a = &ens[0].Records[i]
		case "host=b":
			b = &ens[0].Records[i]
		}
	}
	if a == nil || b == nil {
		t.Fatal("both series should have an ensemble record")
	}
	if a.Kind != "ensemble" || a.Detector != "ensemble" {
		t.Fatalf("ensemble record mislabeled: %+v", a)
	}
	if a.Score <= 62 {
		t.Fatalf("two agreeing detectors should push host=a above the strongest single score (62), got %.1f", a.Score)
	}
	if a.Score <= b.Score {
		t.Fatalf("host=a (two detectors) should outscore host=b (one), got a=%.1f b=%.1f", a.Score, b.Score)
	}
	if len(a.Influencers) != 2 {
		t.Fatalf("ensemble should list its contributing detectors, got %d", len(a.Influencers))
	}
}
