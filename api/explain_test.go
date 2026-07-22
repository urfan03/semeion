package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/explain"
	"github.com/urfan03/semeion/slo"
)

func TestExplainEndpoint(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	base := time.Now().UTC()

	// A deploy plus a symptom on the same service → change-led incident.
	body, _ := json.Marshal(map[string]any{
		"name": "checkout v2", "kind": "deploy", "time": base.Add(-2 * time.Minute),
		"labels": map[string]string{"service": "checkout"},
	})
	do(t, h, http.MethodPost, "/v1/changes", string(body))
	storeSym(s, "checkout-errors", "checkout", base, 90)

	// Open the incident via the tracked view, then grab its id.
	w := do(t, h, http.MethodGet, "/v1/incidents?window=10m", "")
	var inc struct {
		Incidents []struct {
			ID string `json:"id"`
		} `json:"incidents"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &inc)
	if len(inc.Incidents) == 0 {
		t.Fatal("no incident to explain")
	}
	id := inc.Incidents[0].ID

	w = do(t, h, http.MethodGet, "/v1/explain/"+id, "")
	if w.Code != http.StatusOK {
		t.Fatalf("explain: %d %s", w.Code, w.Body)
	}
	var res struct {
		Brief  explain.Brief `json:"brief"`
		Prompt string        `json:"prompt"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Brief.Cause.Kind != "change" || res.Brief.Cause.Target != "checkout v2" {
		t.Fatalf("cause: %+v", res.Brief.Cause)
	}
	if len(res.Brief.Actions) == 0 || !strings.Contains(res.Brief.Actions[0].Title, "Roll back") {
		t.Fatalf("first action should be a rollback: %+v", res.Brief.Actions)
	}
	if !strings.Contains(res.Prompt, "do not invent") {
		t.Errorf("prompt should carry the grounding rules")
	}
}

func TestExplainUnknownIncident(t *testing.T) {
	s := NewServer()
	if w := do(t, s.Handler(), http.MethodGet, "/v1/explain/nope", ""); w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown incident, got %d", w.Code)
	}
}

func TestSLOEndpoint(t *testing.T) {
	s := NewServer()
	now := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)

	var samples []slo.Sample
	for i := 0; i < 1440; i++ {
		ts := now.Add(-time.Duration(1440-i) * time.Minute)
		samples = append(samples, slo.Sample{Time: ts, Total: 1000, Good: 950}) // 5% errors
	}
	body, _ := json.Marshal(map[string]any{
		"objective": 0.999, "window": "24h", "samples": samples, "now": now,
	})
	w := do(t, s.Handler(), http.MethodPost, "/v1/slo", string(body))
	if w.Code != http.StatusOK {
		t.Fatalf("slo: %d %s", w.Code, w.Body)
	}
	var r slo.Report
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Severity != "critical" {
		t.Errorf("5%% errors vs a three-nines budget should be critical, got %q", r.Severity)
	}
	if r.BudgetConsumed <= 1 {
		t.Errorf("budget should be blown, consumed=%.2f", r.BudgetConsumed)
	}
}

// With no explicit clock, the SLO report evaluates as of the freshest sample —
// so a batch of historical data reports on itself, not on wall-clock now.
func TestSLOEvaluatesAsOfLatestSampleByDefault(t *testing.T) {
	s := NewServer()
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	var samples []slo.Sample
	for i := 0; i < 120; i++ {
		samples = append(samples, slo.Sample{Time: old.Add(time.Duration(i) * time.Minute), Total: 100, Good: 100})
	}
	body, _ := json.Marshal(map[string]any{"objective": 0.99, "window": "1h", "samples": samples})
	w := do(t, s.Handler(), http.MethodPost, "/v1/slo", string(body))
	var r slo.Report
	_ = json.Unmarshal(w.Body.Bytes(), &r)
	if r.SLI != 1.0 {
		t.Fatalf("historical clean data should read as perfect (SLI 1.0), got %.3f — clock not anchored to samples", r.SLI)
	}
}

func TestSLORejectsBadMethod(t *testing.T) {
	s := NewServer()
	// GET /v1/slo now lists named SLOs (200); an unsupported method is 405.
	if w := do(t, s.Handler(), http.MethodDelete, "/v1/slo", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for DELETE, got %d", w.Code)
	}
	if w := do(t, s.Handler(), http.MethodGet, "/v1/slo", ""); w.Code != http.StatusOK {
		t.Fatalf("GET /v1/slo should list (200), got %d", w.Code)
	}
}

// Guard the core promise: the brief never invents a field the incident lacks.
func TestExplainDoesNotFabricate(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	// A bare symptom: no service, no change, no template.
	s.Store("mystery", []core.BucketResult{{Time: time.Now().UTC(), Records: []core.Record{{
		Time: time.Now().UTC(), Detector: "count", Series: "", Score: 80, Kind: "metric",
	}}}})
	w := do(t, h, http.MethodGet, "/v1/incidents?window=10m", "")
	var inc struct {
		Incidents []struct{ ID string } `json:"incidents"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &inc)
	if len(inc.Incidents) == 0 {
		t.Skip("no incident formed")
	}
	w = do(t, h, http.MethodGet, "/v1/explain/"+inc.Incidents[0].ID, "")
	var res struct {
		Brief explain.Brief `json:"brief"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	// Cause target falls back to the job, never a made-up service.
	if res.Brief.Cause.Kind == "service" {
		t.Errorf("a symptom with no service must not be explained as a service cause: %+v", res.Brief.Cause)
	}
}
