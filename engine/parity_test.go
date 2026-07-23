package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestSummaryCountField(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	for b := 0; b < 60; b++ {
		n := 100.0
		if b == 50 {
			n = 900
		}
		pts = append(pts, core.DataPoint{Time: t0.Add(time.Duration(b) * time.Minute),
			Values: map[string]float64{"n": n}})
	}
	job := jobspec.Job{Name: "sc", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncCount, SummaryCountField: "n", Side: jobspec.SideHigh}}}
	eng, err := New(job)
	if err != nil {
		t.Fatal(err)
	}
	var hit *core.Record
	for _, br := range eng.Run(pts, 50) {
		if br.Time.Equal(t0.Add(50 * time.Minute)) {
			for i := range br.Records {
				hit = &br.Records[i]
			}
		}
	}
	if hit == nil {
		t.Fatal("pre-aggregated count spike (sum of n) should be flagged")
	}
	if hit.Actual != 900 {
		t.Fatalf("count should use the summary field (900), not len(pts)=1; got %v", hit.Actual)
	}
}

func TestSkipModelUpdate(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	above := 300.0
	mk := func(skip bool) jobspec.Job {
		d := jobspec.Detector{Function: jobspec.FuncMean, Field: "v", Side: jobspec.SideHigh}
		if skip {
			d.Rules = []jobspec.Rule{{SkipActualAbove: &above, SkipModelUpdate: true}}
		}
		return jobspec.Job{Name: "s", BucketSpan: time.Minute, Detectors: []jobspec.Detector{d}}
	}
	pts := func() []core.DataPoint {
		var p []core.DataPoint
		for b := 0; b < 320; b++ {
			v := 100.0
			if b >= 40 {
				v = 600
			}
			p = append(p, core.DataPoint{Time: t0.Add(time.Duration(b) * time.Minute),
				Value: v, Values: map[string]float64{"v": v}})
		}
		return p
	}
	count := func(skip bool) int {
		eng, _ := New(mk(skip))
		n := 0
		for _, br := range eng.Run(pts(), 50) {
			n += len(br.Records)
		}
		return n
	}
	withSkip := count(true)
	without := count(false)
	if withSkip <= without {
		t.Fatalf("skip_model_update should keep flagging the sustained outlier (model not polluted): withSkip=%d without=%d", withSkip, without)
	}
}

func TestRareScoreReflectsRarity(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	for b := 0; b < 300; b++ {
		bt := t0.Add(time.Duration(b) * time.Minute)
		code := "ok"
		if b == 150 {
			code = "veryrare"
		}
		pts = append(pts, core.DataPoint{Time: bt, Fields: map[string]string{"code": code}})
	}
	job := jobspec.Job{Name: "r", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncRare, ByField: "code"}}}
	eng, _ := New(job)
	var score float64
	for _, br := range eng.Run(pts, 50) {
		for _, r := range br.Records {
			if r.Series == "veryrare" {
				score = r.Score
			}
		}
	}
	if score < 80 {
		t.Fatalf("a value appearing in 1 of ~300 buckets should score high (rarity-scaled), got %.1f", score)
	}
}
