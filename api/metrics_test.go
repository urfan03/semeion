package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
)

func TestMetricsExposition(t *testing.T) {
	s := NewServer()
	h := s.Handler()

	do(t, h, http.MethodPost, "/v1/jobs",
		`{"job":{"name":"lat","bucket_span":"1m","detectors":[{"function":"mean","field":"value","side":"high"}]}}`)
	do(t, h, http.MethodPost, "/v1/changes", `{"name":"svc v1","kind":"deploy"}`)
	end := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	sl, _ := json.Marshal(map[string]any{"objective": 0.999, "window": "24h",
		"samples": sloSamples(end, 1440, 1000, 0.05), "now": end})
	do(t, h, http.MethodPost, "/v1/slo/pay", string(sl))

	w := do(t, h, http.MethodGet, "/metrics", "")
	if w.Code != http.StatusOK {
		t.Fatalf("metrics: %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("wrong content-type %q", ct)
	}
	body := w.Body.String()

	for _, want := range []string{
		"# HELP semeion_build_info",
		"# TYPE semeion_live_jobs gauge",
		`semeion_live_jobs{kind="metric"} 1`,
		`semeion_live_jobs{kind="log"} 0`,
		"semeion_changes 1",
		"semeion_incidents_open ",
		"semeion_topology_services ",
		`semeion_slo_burn_rate{slo="pay"}`,
		`semeion_slo_budget_consumed{slo="pay"}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q\n---\n%s", want, body)
		}
	}
}

func TestMetricsReflectIngestAndAlerts(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	do(t, h, http.MethodPost, "/v1/jobs",
		`{"job":{"name":"lat","bucket_span":"1m","detectors":[{"function":"mean","field":"value","side":"high"}]}}`)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	for i := 0; i < 40; i++ {
		v := 100.0
		if i%2 == 0 {
			v = 101
		}
		pts = append(pts, core.DataPoint{Time: base.Add(time.Duration(i) * time.Minute), Value: v})
	}
	body, _ := json.Marshal(map[string]any{"points": pts})
	do(t, h, http.MethodPost, "/v1/jobs/lat/points", string(body))

	m := do(t, h, http.MethodGet, "/metrics", "").Body.String()
	if !strings.Contains(m, `semeion_job_points_ingested{job="lat"} 40`) {
		t.Errorf("point counter not reflected:\n%s", m)
	}
}

func TestMetricsEscapesLabels(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	do(t, h, http.MethodPost, "/v1/jobs",
		`{"job":{"name":"a\"b","bucket_span":"1m","detectors":[{"function":"mean","field":"v"}]}}`)
	m := do(t, h, http.MethodGet, "/metrics", "").Body.String()
	if !strings.Contains(m, `job="a\"b"`) {
		t.Errorf("label not escaped:\n%s", m)
	}
}
