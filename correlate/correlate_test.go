package correlate

import (
	"strings"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
)

var t0 = time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

func sym(job string, offset time.Duration, score float64, ents map[string]string) Symptom {
	return Symptom{Job: job, Time: t0.Add(offset), Detector: "mean(latency)", Score: score, Kind: "metric", Entities: ents}
}

func TestOneIncidentAcrossSignalsWithTheDeployOnTop(t *testing.T) {
	symptoms := []Symptom{
		sym("checkout-errors", 2*time.Minute, 90, map[string]string{"service": "checkout"}),
		{Job: "checkout-logs", Time: t0.Add(3 * time.Minute), Score: 80, Kind: "new",
			Template: "payment gateway timeout after <NUM>s", Entities: map[string]string{"service": "checkout"}},
		sym("cart-latency", 5*time.Minute, 70, map[string]string{"service": "cart"}),
	}
	changes := []Change{{Time: t0, Name: "checkout v2.3.1", Kind: "deploy",
		Labels: map[string]string{"service": "checkout"}}}

	inc := Correlate(symptoms, changes, Options{Window: 10 * time.Minute})
	if len(inc) != 1 {
		t.Fatalf("expected one incident, got %d: %+v", len(inc), inc)
	}
	got := inc[0]
	if len(got.Symptoms) != 3 || len(got.Changes) != 1 {
		t.Fatalf("incident contents: %d symptoms, %d changes", len(got.Symptoms), len(got.Changes))
	}
	if !got.Start.Equal(t0) {
		t.Errorf("the change should pull the incident start back to the deploy, got %s", got.Start)
	}
	top := got.RootCause[0]
	if top.Change == nil || top.Change.Name != "checkout v2.3.1" {
		t.Fatalf("the deploy should rank first, got %+v", top)
	}

	if top.Confidence <= 0 || top.Confidence > 1 {
		t.Errorf("leader confidence should be a share in (0,1], got %v", top.Confidence)
	}
	for _, c := range got.RootCause[1:] {
		if c.Confidence > top.Confidence {
			t.Errorf("leader must have the highest confidence share, got %v > %v", c.Confidence, top.Confidence)
		}
	}
	if !strings.Contains(strings.Join(top.Reasons, "; "), "deliberate change") {
		t.Errorf("reasons must explain the ranking: %v", top.Reasons)
	}
	if !strings.Contains(got.Summary, "likely origin") {
		t.Errorf("summary: %q", got.Summary)
	}
}

func TestUnrelatedSymptomsStaySeparate(t *testing.T) {
	symptoms := []Symptom{
		sym("cpu", 0, 90, map[string]string{"host": "web-1"}),
		sym("cpu", 3*time.Hour, 90, map[string]string{"host": "web-2"}),
	}
	inc := Correlate(symptoms, nil, Options{Window: 10 * time.Minute})
	if len(inc) != 2 {
		t.Fatalf("expected 2 incidents, got %d", len(inc))
	}

	if !inc[0].Start.After(inc[1].Start) {
		t.Error("incidents should be ordered newest first")
	}
}

func TestSameJobDifferentEntitiesAreNotLinked(t *testing.T) {
	symptoms := []Symptom{
		sym("disk", 0, 90, map[string]string{"host": "web-1"}),
		sym("disk", time.Minute, 90, map[string]string{"host": "web-2"}),
	}
	inc := Correlate(symptoms, nil, Options{Window: 10 * time.Minute})
	if len(inc) != 2 {
		t.Fatalf("expected 2 independent incidents, got %d: %+v", len(inc), inc)
	}
}

func TestCrossSignalCoOccurrenceLinks(t *testing.T) {
	symptoms := []Symptom{
		sym("db-latency", 0, 90, map[string]string{"cluster": "db-main"}),
		sym("api-errors", time.Minute, 85, map[string]string{"service": "api"}),
	}
	inc := Correlate(symptoms, nil, Options{Window: 10 * time.Minute})
	if len(inc) != 1 {
		t.Fatalf("expected the co-occurrence to link them, got %d incidents", len(inc))
	}

	far := []Symptom{
		sym("db-latency", 0, 90, map[string]string{"cluster": "db-main"}),
		sym("api-errors", 8*time.Minute, 85, map[string]string{"service": "api"}),
	}
	if inc := Correlate(far, nil, Options{Window: 10 * time.Minute}); len(inc) != 2 {
		t.Fatalf("beyond the co-occurrence window they should stay separate, got %d", len(inc))
	}
}

func TestSharedEntityLinksAcrossTheFullWindow(t *testing.T) {
	symptoms := []Symptom{
		sym("cpu", 0, 90, map[string]string{"host": "web-1"}),
		sym("disk", 9*time.Minute, 60, map[string]string{"host": "web-1"}),
	}
	if inc := Correlate(symptoms, nil, Options{Window: 10 * time.Minute}); len(inc) != 1 {
		t.Fatalf("shared host should link them, got %d incidents", len(inc))
	}
}

func TestEarliestSymptomLeadsWithoutAChange(t *testing.T) {
	symptoms := []Symptom{
		sym("db-saturation", 0, 60, map[string]string{"host": "db-1"}),
		sym("api-latency", 2*time.Minute, 95, map[string]string{"host": "db-1"}),
	}
	inc := Correlate(symptoms, nil, Options{Window: 10 * time.Minute})
	if len(inc) != 1 {
		t.Fatalf("expected one incident, got %d", len(inc))
	}

	if inc[0].RootCause[0].Symptom.Job != "db-saturation" {
		t.Fatalf("the earliest symptom should lead, got %+v", inc[0].RootCause[0].Symptom)
	}
}

func TestMinScoreFiltersBeforeGrouping(t *testing.T) {
	symptoms := []Symptom{
		sym("noise", 0, 20, map[string]string{"host": "web-1"}),
		sym("real", time.Minute, 90, map[string]string{"host": "web-1"}),
	}
	inc := Correlate(symptoms, nil, Options{Window: 10 * time.Minute, MinScore: 50})
	if len(inc) != 1 || len(inc[0].Symptoms) != 1 {
		t.Fatalf("low-score symptom should have been dropped: %+v", inc)
	}
}

func TestFromRecordsLiftsInfluencersToEntities(t *testing.T) {
	res := []core.BucketResult{{
		Time: t0,
		Records: []core.Record{{
			Time: t0, Detector: "mean(latency)", Series: "web-1", Score: 88, Kind: "metric",
			Influencers: []core.Influencer{{Field: "host", Value: "web-1"}},
		}},
	}}
	got := FromRecords("latency", res)
	if len(got) != 1 {
		t.Fatalf("expected 1 symptom, got %d", len(got))
	}
	if got[0].Entities["host"] != "web-1" || got[0].Job != "latency" {
		t.Fatalf("symptom: %+v", got[0])
	}
}

func TestEmptyInput(t *testing.T) {
	if inc := Correlate(nil, nil, Options{}); inc != nil {
		t.Fatalf("expected no incidents, got %+v", inc)
	}
}

func TestLoneChangeIsNotAnIncident(t *testing.T) {
	changes := []Change{{Time: t0, Name: "svc v1", Kind: "deploy", Labels: map[string]string{"service": "svc"}}}
	if inc := Correlate(nil, changes, Options{Window: 10 * time.Minute}); len(inc) != 0 {
		t.Fatalf("a lone change must not surface as an incident, got %+v", inc)
	}

	syms := []Symptom{sym("errors", 2*time.Minute, 90, map[string]string{"service": "svc"})}
	inc := Correlate(syms, changes, Options{Window: 10 * time.Minute})
	if len(inc) != 1 || len(inc[0].Changes) != 1 {
		t.Fatalf("a symptom near the change should form one incident that keeps it, got %+v", inc)
	}
}
