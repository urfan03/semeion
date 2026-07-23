package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestRuleSkipDiffRatio(t *testing.T) {
	ratio := 0.10
	d := jobspec.Detector{Function: jobspec.FuncMean, Field: "v", Side: jobspec.SideHigh,
		Rules: []jobspec.Rule{{SkipDiffRatioBelow: &ratio}}}
	e := &Engine{job: jobspec.Job{Detectors: []jobspec.Detector{d}}}

	tiny := core.Record{Actual: 103, Typical: 100}
	big := core.Record{Actual: 180, Typical: 100}
	if !e.suppressed(d, tiny) {
		t.Fatal("a 3% deviation should be suppressed by a 10% diff-ratio floor")
	}
	if e.suppressed(d, big) {
		t.Fatal("an 80% deviation must not be suppressed")
	}
}

func TestRuleSkipHours(t *testing.T) {
	d := jobspec.Detector{Function: jobspec.FuncCount,
		Rules: []jobspec.Rule{{SkipHoursUTC: []int{2, 3}}}}
	e := &Engine{job: jobspec.Job{Detectors: []jobspec.Detector{d}}}
	at := func(h int) core.Record {
		return core.Record{Time: time.Date(2026, 1, 1, h, 30, 0, 0, time.UTC), Actual: 999}
	}
	if !e.suppressed(d, at(2)) {
		t.Fatal("02:30 should be muted")
	}
	if e.suppressed(d, at(10)) {
		t.Fatal("10:30 must not be muted")
	}
}

func TestRuleSkipInfluencer(t *testing.T) {
	d := jobspec.Detector{Function: jobspec.FuncMean, Field: "v",
		Rules: []jobspec.Rule{{SkipInfluencer: map[string][]string{"env": {"staging"}}}}}
	e := &Engine{job: jobspec.Job{Detectors: []jobspec.Detector{d}}}
	staging := core.Record{Actual: 500, Typical: 100,
		Influencers: []core.Influencer{{Field: "env", Value: "staging"}}}
	prod := core.Record{Actual: 500, Typical: 100,
		Influencers: []core.Influencer{{Field: "env", Value: "prod"}}}
	if !e.suppressed(d, staging) {
		t.Fatal("env=staging should be safelisted (suppressed)")
	}
	if e.suppressed(d, prod) {
		t.Fatal("env=prod must not be suppressed")
	}
}
