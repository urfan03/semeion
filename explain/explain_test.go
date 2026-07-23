package explain

import (
	"strings"
	"testing"
	"time"

	"github.com/urfan03/semeion/correlate"
)

var t0 = time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

func TestExplainChangeLedIncidentRecommendsRollback(t *testing.T) {
	inc := correlate.Incident{
		ID: "inc-1", Start: t0, End: t0.Add(5 * time.Minute),
		Symptoms: []correlate.Symptom{{Job: "checkout-errors"}, {Job: "cart-latency"}},
		Jobs:     []string{"checkout-errors", "cart-latency"},
		Services: []string{"checkout", "cart"},
		RootCause: []correlate.Candidate{{
			Change:     &correlate.Change{Name: "checkout v2.3.1", Kind: "deploy"},
			Confidence: 1.0,
			Reasons:    []string{"deliberate change (checkout v2.3.1)", "first event of the incident"},
		}},
	}
	b := Explain(inc)

	if b.Cause.Kind != "change" || b.Cause.Target != "checkout v2.3.1" {
		t.Fatalf("cause: %+v", b.Cause)
	}
	if len(b.Actions) == 0 || b.Actions[0].Priority != 1 || !strings.Contains(b.Actions[0].Title, "Roll back") {
		t.Fatalf("the first action should be a rollback, got %+v", b.Actions)
	}
	if !strings.Contains(b.Narrative, "checkout v2.3.1") {
		t.Errorf("narrative should name the change: %q", b.Narrative)
	}

	for _, a := range b.Actions {
		if a.Rationale == "" {
			t.Errorf("action %q has no rationale", a.Title)
		}
	}
}

func TestExplainTopologyOriginRecommendsInvestigatingUpstream(t *testing.T) {
	inc := correlate.Incident{
		ID: "inc-2", Start: t0, End: t0.Add(time.Minute),
		Symptoms: []correlate.Symptom{{Job: "db"}, {Job: "api"}},
		Jobs:     []string{"db", "api"},
		Services: []string{"payments-db", "checkout", "gateway"},
		RootCause: []correlate.Candidate{{
			Symptom:    correlate.Symptom{Job: "db-latency", Detector: "mean(latency)", Entities: map[string]string{"service": "payments-db"}},
			Confidence: 1.0,
			Reasons:    []string{"upstream of 2 of the 2 other affected service(s)"},
		}},
	}
	b := Explain(inc)

	if b.Cause.Kind != "service" || b.Cause.Target != "payments-db" {
		t.Fatalf("cause: %+v", b.Cause)
	}
	if len(b.Actions) == 0 || !strings.Contains(b.Actions[0].Title, "payments-db") {
		t.Fatalf("should recommend investigating the upstream service, got %+v", b.Actions)
	}
	if !strings.Contains(b.Actions[0].Rationale, "upstream of") {
		t.Errorf("rationale should cite the topological evidence: %q", b.Actions[0].Rationale)
	}
}

func TestExplainNewLogTemplate(t *testing.T) {
	inc := correlate.Incident{
		ID: "inc-3", Start: t0, End: t0,
		Symptoms: []correlate.Symptom{{Job: "logs"}},
		Jobs:     []string{"logs"},
		RootCause: []correlate.Candidate{{
			Symptom:    correlate.Symptom{Job: "logs", Kind: "new", Template: "payment gateway timeout after <NUM>s"},
			Confidence: 0.9,
			Reasons:    []string{"novel log template", "first event of the incident"},
		}},
	}
	b := Explain(inc)
	if b.Cause.Kind != "log" {
		t.Fatalf("cause kind: %q", b.Cause.Kind)
	}
	found := false
	for _, a := range b.Actions {
		if strings.Contains(a.Rationale, "payment gateway timeout") {
			found = true
		}
	}
	if !found {
		t.Fatalf("an action should quote the new template: %+v", b.Actions)
	}
}

func TestExplainNoRootCause(t *testing.T) {
	b := Explain(correlate.Incident{ID: "inc-x", Start: t0, End: t0})
	if b.Cause.Kind != "unknown" {
		t.Fatalf("expected unknown cause, got %q", b.Cause.Kind)
	}
	if b.Narrative == "" || b.Headline == "" {
		t.Error("even a causeless incident needs a headline and narrative")
	}
}

func TestPromptIsGroundedAndForbidsInvention(t *testing.T) {
	inc := correlate.Incident{
		ID: "inc-4", Start: t0, End: t0.Add(time.Minute),
		Symptoms: []correlate.Symptom{{Job: "a"}},
		Jobs:     []string{"a"},
		RootCause: []correlate.Candidate{{
			Change: &correlate.Change{Name: "svc v1", Kind: "deploy"}, Confidence: 1,
			Reasons: []string{"deliberate change (svc v1)"},
		}},
	}
	p := Prompt(Explain(inc))
	for _, must := range []string{"do not invent", "svc v1", "Recommended actions", "Confidence"} {
		if !strings.Contains(p, must) {
			t.Errorf("prompt missing %q:\n%s", must, p)
		}
	}
}

func TestTemplateNarratorIsIdentity(t *testing.T) {
	b := Brief{Narrative: "hello"}
	if got := (TemplateNarrator{}).Narrate(b); got != "hello" {
		t.Fatalf("template narrator should return the brief narrative, got %q", got)
	}
}
