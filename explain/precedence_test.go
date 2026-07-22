package explain

import (
	"strings"
	"testing"
	"time"

	"github.com/urfan03/semeion/correlate"
)

// C6 regression: a change that lands AFTER symptoms began is a remediation, not
// a cause. It must not be recommended for rollback, and the brief must not claim
// it "preceded" the incident.
func TestMidIncidentChangeNotRecommendedForRollback(t *testing.T) {
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	inc := correlate.Incident{
		ID: "inc-1", Start: base, End: base.Add(10 * time.Minute),
		Symptoms: []correlate.Symptom{{Job: "errors", Time: base, Score: 90,
			Entities: map[string]string{"service": "checkout"}}},
		Jobs: []string{"errors"},
		// The change is the "lead" candidate here, but it happened 9 minutes in.
		RootCause: []correlate.Candidate{{
			Change:     &correlate.Change{Name: "checkout hotfix", Kind: "deploy", Time: base.Add(9 * time.Minute)},
			Confidence: 1.0,
			Reasons:    []string{"change during the incident (checkout hotfix), after symptoms began — not a likely cause"},
		}},
	}
	b := Explain(inc)
	for _, a := range b.Actions {
		if strings.Contains(a.Title, "Roll back") {
			t.Fatalf("must not recommend rolling back a mid-incident change: %+v", a)
		}
	}
	if strings.Contains(b.Narrative, "preced") {
		t.Errorf("narrative must not claim the change preceded the incident: %q", b.Narrative)
	}
}

// A change that genuinely preceded the onset still earns a rollback.
func TestPrecedingChangeStillRecommendsRollback(t *testing.T) {
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	inc := correlate.Incident{
		ID: "inc-2", Start: base.Add(-2 * time.Minute), End: base.Add(5 * time.Minute),
		Symptoms: []correlate.Symptom{{Job: "errors", Time: base, Score: 90,
			Entities: map[string]string{"service": "checkout"}}},
		Jobs: []string{"errors"},
		RootCause: []correlate.Candidate{{
			Change:     &correlate.Change{Name: "checkout v2", Kind: "deploy", Time: base.Add(-2 * time.Minute)},
			Confidence: 1.0,
			Reasons:    []string{"deliberate change preceding the incident (checkout v2)"},
		}},
	}
	b := Explain(inc)
	found := false
	for _, a := range b.Actions {
		if strings.Contains(a.Title, "Roll back checkout v2") {
			found = true
		}
	}
	if !found {
		t.Fatalf("a preceding change should still be recommended for rollback: %+v", b.Actions)
	}
}
