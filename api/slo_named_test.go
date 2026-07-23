package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/urfan03/semeion/slo"
)

func sloSamples(end time.Time, n int, total, errRatio float64) []slo.Sample {
	s := make([]slo.Sample, n)
	for i := 0; i < n; i++ {
		s[i] = slo.Sample{
			Time:  end.Add(-time.Duration(n-i) * time.Minute),
			Total: total, Good: total * (1 - errRatio),
		}
	}
	return s
}

func TestNamedSLOAccumulatesAndReports(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	end := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)

	body, _ := json.Marshal(map[string]any{
		"objective": 0.999, "window": "24h",
		"samples": sloSamples(end.Add(-12*time.Hour), 720, 1000, 0.05), "now": end.Add(-12 * time.Hour),
	})
	if w := do(t, h, http.MethodPost, "/v1/slo/checkout-availability", string(body)); w.Code != http.StatusOK {
		t.Fatalf("append 1: %d %s", w.Code, w.Body)
	}

	body, _ = json.Marshal(map[string]any{
		"samples": sloSamples(end, 720, 1000, 0.05), "now": end,
	})
	w := do(t, h, http.MethodPost, "/v1/slo/checkout-availability", string(body))
	if w.Code != http.StatusOK {
		t.Fatalf("append 2: %d %s", w.Code, w.Body)
	}
	var rep slo.Report
	_ = json.Unmarshal(w.Body.Bytes(), &rep)
	if rep.Objective != 0.999 {
		t.Errorf("target objective should persist across appends, got %v", rep.Objective)
	}
	if rep.Severity != "critical" {
		t.Errorf("5%% errors vs three-nines should be critical, got %q", rep.Severity)
	}

	w = do(t, h, http.MethodGet, "/v1/slo/checkout-availability", "")
	var got slo.Report
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Severity != "critical" {
		t.Errorf("GET should reflect the accumulated series, got %q", got.Severity)
	}
}

func TestSLOListing(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	end := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name string
		err  float64
	}{{"good-svc", 0.0001}, {"bad-svc", 0.05}} {
		body, _ := json.Marshal(map[string]any{
			"objective": 0.999, "window": "24h",
			"samples": sloSamples(end, 1440, 1000, tc.err), "now": end,
		})
		do(t, h, http.MethodPost, "/v1/slo/"+tc.name, string(body))
	}

	w := do(t, h, http.MethodGet, "/v1/slo", "")
	var res struct {
		SLOs []struct {
			Name     string  `json:"name"`
			Severity string  `json:"severity"`
			SLI      float64 `json:"sli"`
		} `json:"slos"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.SLOs) != 2 {
		t.Fatalf("expected 2 SLOs listed, got %d", len(res.SLOs))
	}
	sev := map[string]string{}
	for _, r := range res.SLOs {
		sev[r.Name] = r.Severity
	}
	if sev["good-svc"] != "ok" || sev["bad-svc"] != "critical" {
		t.Fatalf("listing severities wrong: %+v", sev)
	}
}

func TestSLOWindowTrimsOldSamples(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	end := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)

	body, _ := json.Marshal(map[string]any{
		"objective": 0.99, "window": "1h",
		"samples": sloSamples(end, 600, 100, 0.001), "now": end,
	})
	do(t, h, http.MethodPost, "/v1/slo/trim", string(body))

	series := s.sloSeries("trim", false)
	if series == nil {
		t.Fatal("series not created")
	}
	series.mu.Lock()
	n := len(series.samples)
	series.mu.Unlock()
	if n >= 600 {
		t.Fatalf("old samples beyond 2× window should be trimmed, kept %d", n)
	}
	if n == 0 {
		t.Fatal("trimming must keep the recent window")
	}
}

func TestSLOStatelessStillWorks(t *testing.T) {
	s := NewServer()
	end := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	body, _ := json.Marshal(map[string]any{
		"objective": 0.999, "window": "24h",
		"samples": sloSamples(end, 1440, 1000, 0.05), "now": end,
	})
	w := do(t, s.Handler(), http.MethodPost, "/v1/slo", string(body))
	var rep slo.Report
	_ = json.Unmarshal(w.Body.Bytes(), &rep)
	if rep.Severity != "critical" {
		t.Fatalf("stateless eval still expected, got %q", rep.Severity)
	}

	if s.sloSeries("", false) != nil {
		t.Error("stateless eval should not persist anything")
	}
}

func TestUnknownNamedSLO(t *testing.T) {
	s := NewServer()
	if w := do(t, s.Handler(), http.MethodGet, "/v1/slo/ghost", ""); w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown SLO, got %d", w.Code)
	}
}

func TestSLODefaultClockIsLatestSample(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	old := time.Date(2020, 6, 1, 0, 0, 0, 0, time.UTC)

	body, _ := json.Marshal(map[string]any{
		"objective": 0.99, "window": "1h",
		"samples": sloSamples(old, 120, 100, 0.0),
	})
	w := do(t, h, http.MethodPost, "/v1/slo/historical", string(body))
	var rep slo.Report
	_ = json.Unmarshal(w.Body.Bytes(), &rep)
	if rep.SLI != 1.0 {
		t.Fatalf("historical clean data should read as perfect, got SLI %.3f", rep.SLI)
	}
}
