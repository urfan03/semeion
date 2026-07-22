package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/correlate"
)

func recordAt(ts time.Time, detector, host string, score float64) core.BucketResult {
	return core.BucketResult{Time: ts, Records: []core.Record{{
		Time: ts, Detector: detector, Series: host, Score: score, Kind: "metric",
		Influencers: []core.Influencer{{Field: "service", Value: host}},
	}}}
}

func TestIncidentsCorrelateStoredResultsWithAChange(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	s.Store("checkout-errors", []core.BucketResult{recordAt(base.Add(2*time.Minute), "count", "checkout", 90)})
	s.Store("cart-latency", []core.BucketResult{recordAt(base.Add(4*time.Minute), "mean(latency)", "cart", 70)})

	// A CI pipeline posts the deploy.
	body, _ := json.Marshal(correlate.Change{
		Time: base, Name: "checkout v2.3.1", Kind: "deploy",
		Labels: map[string]string{"service": "checkout"},
	})
	if w := do(t, h, http.MethodPost, "/v1/changes", string(body)); w.Code != http.StatusCreated {
		t.Fatalf("record change: %d %s", w.Code, w.Body)
	}

	w := do(t, h, http.MethodGet, "/v1/incidents?window=10m", "")
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var res struct {
		Symptoms  int                  `json:"symptoms"`
		Incidents []correlate.Incident `json:"incidents"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Symptoms != 2 {
		t.Fatalf("expected 2 symptoms, got %d", res.Symptoms)
	}
	if len(res.Incidents) != 1 {
		t.Fatalf("the two jobs plus the deploy should be one incident, got %d", len(res.Incidents))
	}
	inc := res.Incidents[0]
	if len(inc.Jobs) != 2 {
		t.Errorf("incident should span both jobs: %v", inc.Jobs)
	}
	if len(inc.RootCause) == 0 || inc.RootCause[0].Change == nil {
		t.Fatalf("the deploy should lead the ranking: %+v", inc.RootCause)
	}
	if !inc.Start.Equal(base) {
		t.Errorf("incident should start at the deploy, got %s", inc.Start)
	}
}

func TestCorrelateEndpointWithSuppliedSymptoms(t *testing.T) {
	s := NewServer()
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	body, _ := json.Marshal(map[string]any{
		"window": "5m",
		"symptoms": []correlate.Symptom{
			{Job: "a", Time: base, Score: 80, Entities: map[string]string{"host": "web-1"}},
			{Job: "b", Time: base.Add(time.Minute), Score: 90, Entities: map[string]string{"host": "web-1"}},
			{Job: "c", Time: base.Add(2 * time.Hour), Score: 95, Entities: map[string]string{"host": "web-9"}},
		},
	})
	w := do(t, s.Handler(), http.MethodPost, "/v1/correlate", string(body))
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var res struct {
		Incidents []correlate.Incident `json:"incidents"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if len(res.Incidents) != 2 {
		t.Fatalf("expected 2 incidents (one pair + one lone), got %d", len(res.Incidents))
	}
}

func TestCorrelateRejectsABadWindow(t *testing.T) {
	s := NewServer()
	w := do(t, s.Handler(), http.MethodPost, "/v1/correlate", `{"window":"ten minutes","symptoms":[]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unparseable window, got %d %s", w.Code, w.Body)
	}
}

func TestChangesRequireAName(t *testing.T) {
	s := NewServer()
	if w := do(t, s.Handler(), http.MethodPost, "/v1/changes", `{"kind":"deploy"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a nameless change, got %d", w.Code)
	}
}

// A change posted without a timestamp is happening now — that is the normal
// case for a CI hook, and it must not land at the zero time.
func TestChangeDefaultsToNow(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	if w := do(t, h, http.MethodPost, "/v1/changes", `{"name":"api v9","kind":"deploy"}`); w.Code != http.StatusCreated {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w := do(t, h, http.MethodGet, "/v1/changes", "")
	var res struct {
		Changes []correlate.Change `json:"changes"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if len(res.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(res.Changes))
	}
	if res.Changes[0].Time.IsZero() || time.Since(res.Changes[0].Time) > time.Minute {
		t.Fatalf("timestamp should default to now, got %s", res.Changes[0].Time)
	}
}
