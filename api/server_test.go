package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
)

func analyzeBody(t *testing.T) []byte {
	t.Helper()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	for i := 0; i < 60; i++ {
		v := 100.0
		if i == 50 {
			v = 900
		}
		pts = append(pts, core.DataPoint{Time: start.Add(time.Duration(i) * time.Minute), Value: v})
	}
	req := map[string]any{
		"job": map[string]any{
			"name":        "apitest",
			"bucket_span": "1m",
			"detectors":   []map[string]any{{"function": "mean", "field": "v"}},
		},
		"points":    pts,
		"threshold": 50,
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAnalyzeResultsGrafanaUI(t *testing.T) {
	srv := httptest.NewServer(NewServer().Handler())
	defer srv.Close()

	// analyze
	resp, err := http.Post(srv.URL+"/v1/analyze", "application/json", bytes.NewReader(analyzeBody(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("analyze status %d", resp.StatusCode)
	}
	var ar analyzeResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		t.Fatal(err)
	}
	if ar.Job != "apitest" || ar.Records == 0 {
		t.Fatalf("analyze: job=%q records=%d (want apitest, >0)", ar.Job, ar.Records)
	}

	// results
	r2, err := http.Get(srv.URL + "/v1/results/apitest")
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	if r2.StatusCode != 200 {
		t.Fatalf("results status %d", r2.StatusCode)
	}

	// grafana series
	r3, err := http.Get(srv.URL + "/v1/grafana/apitest")
	if err != nil {
		t.Fatal(err)
	}
	defer r3.Body.Close()
	var series []struct {
		Time  int64   `json:"time"`
		Score float64 `json:"score"`
	}
	if err := json.NewDecoder(r3.Body).Decode(&series); err != nil {
		t.Fatal(err)
	}
	if len(series) == 0 || series[0].Time == 0 {
		t.Fatalf("grafana series empty or missing time: %+v", series)
	}

	// UI
	r4, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer r4.Body.Close()
	page, err := io.ReadAll(r4.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "Anomaly Explorer") {
		t.Fatal("UI did not render the explorer page")
	}
}

func TestAutopilotEndpoint(t *testing.T) {
	srv := httptest.NewServer(NewServer().Handler())
	defer srv.Close()

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	for i := 0; i < 90; i++ {
		pts = append(pts, core.DataPoint{Time: start.Add(time.Duration(i) * time.Minute),
			Values: map[string]float64{"cpu": 50, "mem": 60}})
	}
	body, _ := json.Marshal(map[string]any{"points": pts})
	resp, err := http.Post(srv.URL+"/v1/autopilot", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		BucketSpan string           `json:"bucket_span"`
		Detectors  []map[string]any `json:"detectors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.BucketSpan != "1m0s" || len(out.Detectors) == 0 {
		t.Fatalf("autopilot: span=%q detectors=%d", out.BucketSpan, len(out.Detectors))
	}
}

func TestAnalyzeBadJob(t *testing.T) {
	srv := httptest.NewServer(NewServer().Handler())
	defer srv.Close()
	body := []byte(`{"job":{"name":"x","bucket_span":"nope","detectors":[]},"points":[]}`)
	resp, err := http.Post(srv.URL+"/v1/analyze", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for a bad job, got %d", resp.StatusCode)
	}
}
