package engine

import (
	"testing"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestRuleScopeGatesSuppression(t *testing.T) {
	above := 0.0
	rule := jobspec.Rule{
		SkipActualAbove: &above,
		Scope:           []jobspec.ScopeClause{{Field: "host", FilterID: "f", Include: true}},
		ResolvedFilters: map[string][]string{"f": {"safe"}},
	}
	d := jobspec.Detector{Function: jobspec.FuncMean, Field: "v", ByField: "host", Rules: []jobspec.Rule{rule}}
	e := &Engine{job: jobspec.Job{Detectors: []jobspec.Detector{d}}}

	safe := core.Record{Actual: 5, Influencers: []core.Influencer{{Field: "host", Value: "safe"}}}
	if !e.suppressed(d, safe) {
		t.Fatal("an in-scope (include) record must be suppressed by the scoped rule")
	}
	other := core.Record{Actual: 5, Influencers: []core.Influencer{{Field: "host", Value: "other"}}}
	if e.suppressed(d, other) {
		t.Fatal("an out-of-scope record must NOT be suppressed by the scoped rule")
	}
}
