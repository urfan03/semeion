package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/urfan03/semeion/alert"
	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/correlate"
)

func storeSym(s *Server, job, service string, ts time.Time, score float64) {
	s.Store(job, []core.BucketResult{{Time: ts, Records: []core.Record{{
		Time: ts, Detector: "mean(latency)", Series: service, Score: score, Kind: "metric",
		Influencers: []core.Influencer{{Field: "service", Value: service}},
	}}}})
}

func TestIncidentsTrackedViewIsStable(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	now := time.Now().UTC()
	storeSym(s, "checkout", "checkout", now, 90)

	w := do(t, h, http.MethodGet, "/v1/incidents?window=10m", "")
	var res struct {
		Incidents []correlate.Tracked `json:"incidents"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Incidents) != 1 || res.Incidents[0].Status != correlate.StatusOpen {
		t.Fatalf("expected one open incident, got %+v", res.Incidents)
	}
	id := res.Incidents[0].ID
	if id == "" {
		t.Fatal("tracked incident needs a stable id")
	}

	w = do(t, h, http.MethodGet, "/v1/incidents?window=10m", "")
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if len(res.Incidents) != 1 || res.Incidents[0].ID != id {
		t.Fatalf("id should be stable across calls: %+v", res.Incidents)
	}

	if w := do(t, h, http.MethodGet, "/v1/incidents/open", ""); w.Code != http.StatusOK {
		t.Fatalf("/open: %d", w.Code)
	}
}

func TestStatelessViewSkipsTheTracker(t *testing.T) {
	s := NewServer()
	now := time.Now().UTC()
	storeSym(s, "checkout", "checkout", now, 90)

	w := do(t, s.Handler(), http.MethodGet, "/v1/incidents?stateless=1", "")
	var res struct {
		Incidents []correlate.Incident `json:"incidents"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if len(res.Incidents) != 1 {
		t.Fatalf("expected one incident, got %d", len(res.Incidents))
	}
	if res.Incidents[0].Status != "" {
		t.Errorf("a stateless correlation must not carry lifecycle status, got %q", res.Incidents[0].Status)
	}

	if w := do(t, s.Handler(), http.MethodGet, "/v1/incidents/open", ""); w.Code == http.StatusOK {
		var open struct {
			Incidents []correlate.Tracked `json:"incidents"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &open)
		if len(open.Incidents) != 0 {
			t.Errorf("stateless view should not have opened anything: %+v", open.Incidents)
		}
	}
}

func TestLiveIngestOpensIncidentAndAlertsOnce(t *testing.T) {
	var (
		mu    sync.Mutex
		fired []alert.Alert
		hook  = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var a alert.Alert
			_ = json.NewDecoder(r.Body).Decode(&a)
			mu.Lock()
			fired = append(fired, a)
			mu.Unlock()
		}))
	)
	defer hook.Close()

	s := NewServer().WithNotifier(alert.NewNotifier(alert.NewWebhookSink(hook.URL)))
	h := s.Handler()

	if w := do(t, h, http.MethodPost, "/v1/jobs",
		`{"job":{"name":"checkout","bucket_span":"1m","detectors":[{"function":"mean","field":"value","side":"high"}]},"metric":"m"}`); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}

	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	push := func(spikeAt int) {
		var pts []core.DataPoint
		for i := 0; i < 60; i++ {
			v := 100.0
			if i%2 == 0 {
				v = 101
			}
			if i == spikeAt {
				v = 900
			}
			pts = append(pts, core.DataPoint{Time: start.Add(time.Duration(i) * time.Minute), Value: v,
				Fields: map[string]string{"service": "checkout"}})
		}
		body, _ := json.Marshal(map[string]any{"points": pts})
		do(t, h, http.MethodPost, "/v1/jobs/checkout/points", string(body))
	}
	push(50)

	mu.Lock()
	incidentAlerts := 0
	for _, a := range fired {
		if a.Kind == "incident" && a.Detector == "opened" {
			incidentAlerts++
		}
	}
	mu.Unlock()
	if incidentAlerts != 1 {
		t.Fatalf("expected exactly one incident-opened alert, got %d (all: %+v)", incidentAlerts, fired)
	}

	w := do(t, h, http.MethodGet, "/v1/incidents/open", "")
	var open struct {
		Incidents []correlate.Tracked `json:"incidents"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &open)
	if len(open.Incidents) != 1 {
		t.Fatalf("expected one open incident after ingest, got %d", len(open.Incidents))
	}
}
