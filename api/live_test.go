package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/urfan03/semeion/alert"
	"github.com/urfan03/semeion/core"
)

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// otlpMetrics renders an OTLP/JSON export of `n` points on one metric, with a
// spike at spikeAt (use -1 for none).
func otlpMetrics(metric string, start time.Time, n, spikeAt int) string {
	var dps []string
	for i := 0; i < n; i++ {
		v := 100.0
		if i%2 == 0 {
			v = 101.0
		}
		if i == spikeAt {
			v = 900
		}
		dps = append(dps, fmt.Sprintf(
			`{"timeUnixNano":"%d","asDouble":%g,"attributes":[{"key":"host","value":{"stringValue":"web-1"}}]}`,
			start.Add(time.Duration(i)*time.Minute).UnixNano(), v))
	}
	return fmt.Sprintf(`{"resourceMetrics":[{"resource":{"attributes":[]},"scopeMetrics":[{"metrics":[
	  {"name":%q,"gauge":{"dataPoints":[%s]}}]}]}]}`, metric, strings.Join(dps, ","))
}

func TestLiveJobLifecycle(t *testing.T) {
	s := NewServer()
	h := s.Handler()

	create := `{"job":{"name":"live-latency","bucket_span":"1m",
	  "detectors":[{"function":"mean","field":"value","side":"high"}]},
	  "metric":"http.server.duration","threshold":50}`
	if w := do(t, h, http.MethodPost, "/v1/jobs", create); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}

	// The job appears in the listing as live.
	w := do(t, h, http.MethodGet, "/v1/jobs", "")
	var jobs struct {
		Jobs []string `json:"jobs"`
		Live []string `json:"live"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &jobs)
	if len(jobs.Live) != 1 || jobs.Live[0] != "live-latency" {
		t.Fatalf("live listing: %+v", jobs)
	}

	// Push points directly.
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	for i := 0; i < 60; i++ {
		v := 100.0
		if i%2 == 0 {
			v = 101.0
		}
		if i == 50 {
			v = 900
		}
		pts = append(pts, core.DataPoint{Time: start.Add(time.Duration(i) * time.Minute), Value: v})
	}
	body, _ := json.Marshal(map[string]any{"points": pts})
	w = do(t, h, http.MethodPost, "/v1/jobs/live-latency/points", string(body))
	if w.Code != http.StatusOK {
		t.Fatalf("push: %d %s", w.Code, w.Body)
	}
	var pushed struct {
		Anomalies []core.BucketResult `json:"anomalies"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &pushed)
	if len(pushed.Anomalies) == 0 {
		t.Fatal("the spike produced no anomaly")
	}

	// Status reflects the ingestion.
	w = do(t, h, http.MethodGet, "/v1/jobs/live-latency", "")
	var st map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if st["points"].(float64) != 60 {
		t.Errorf("points counter: %v", st["points"])
	}

	// Results are queryable through the existing endpoints.
	if w := do(t, h, http.MethodGet, "/v1/results/live-latency", ""); w.Code != http.StatusOK {
		t.Errorf("results: %d", w.Code)
	}
	if w := do(t, h, http.MethodGet, "/v1/grafana/live-latency", ""); w.Code != http.StatusOK {
		t.Errorf("grafana: %d", w.Code)
	}

	// Delete removes it.
	if w := do(t, h, http.MethodDelete, "/v1/jobs/live-latency", ""); w.Code != http.StatusOK {
		t.Errorf("delete: %d", w.Code)
	}
	if w := do(t, h, http.MethodGet, "/v1/jobs/live-latency", ""); w.Code != http.StatusNotFound {
		t.Errorf("deleted job should be gone, got %d", w.Code)
	}
}

func TestOTLPMetricsIngestAlerts(t *testing.T) {
	var (
		mu   sync.Mutex
		got  []alert.Alert
		hook = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var a alert.Alert
			_ = json.NewDecoder(r.Body).Decode(&a)
			mu.Lock()
			got = append(got, a)
			mu.Unlock()
		}))
	)
	defer hook.Close()

	s := NewServer().WithNotifier(alert.NewNotifier(alert.NewWebhookSink(hook.URL)))
	h := s.Handler()

	create := `{"job":{"name":"otlp-latency","bucket_span":"1m",
	  "detectors":[{"function":"mean","field":"value","side":"high"}]},
	  "metric":"http.server.duration"}`
	if w := do(t, h, http.MethodPost, "/v1/jobs", create); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w := do(t, h, http.MethodPost, "/v1/otlp/v1/metrics", otlpMetrics("http.server.duration", start, 60, 50))
	if w.Code != http.StatusOK {
		t.Fatalf("otlp: %d %s", w.Code, w.Body)
	}
	var res struct {
		Accepted, Jobs, Anomalies int
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.Accepted != 60 || res.Jobs != 1 {
		t.Fatalf("otlp routing: %+v", res)
	}
	if res.Anomalies == 0 {
		t.Fatal("no anomaly from the injected spike")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 || got[0].Job != "otlp-latency" {
		t.Fatalf("alert not delivered: %+v", got)
	}
}

// A metric nobody claims must be accepted and ignored, not error.
func TestOTLPUnclaimedMetric(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	create := `{"job":{"name":"only-cpu","bucket_span":"1m",
	  "detectors":[{"function":"mean","field":"value"}]},"metric":"system.cpu"}`
	do(t, h, http.MethodPost, "/v1/jobs", create)

	w := do(t, h, http.MethodPost, "/v1/otlp/v1/metrics",
		otlpMetrics("something.else", time.Now(), 5, -1))
	if w.Code != http.StatusOK {
		t.Fatalf("code: %d %s", w.Code, w.Body)
	}
	var res struct{ Accepted, Jobs int }
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.Accepted != 5 || res.Jobs != 0 {
		t.Fatalf("unclaimed metric should route nowhere: %+v", res)
	}
}

func TestOTLPLogsIngest(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	if w := do(t, h, http.MethodPost, "/v1/jobs",
		`{"logs":true,"name":"live-logs","bucket_span":"1m"}`); w.Code != http.StatusCreated {
		t.Fatalf("create logs job: %d %s", w.Code, w.Body)
	}

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var recs []string
	for i := 0; i < 30; i++ {
		msg := "request served for user alice"
		if i == 25 {
			msg = "disk controller reset unexpectedly on volume vg0"
		}
		recs = append(recs, fmt.Sprintf(`{"timeUnixNano":"%d","body":{"stringValue":%q}}`,
			start.Add(time.Duration(i)*time.Minute).UnixNano(), msg))
	}
	payload := fmt.Sprintf(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[%s]}]}]}`, strings.Join(recs, ","))

	w := do(t, h, http.MethodPost, "/v1/otlp/v1/logs", payload)
	if w.Code != http.StatusOK {
		t.Fatalf("otlp logs: %d %s", w.Code, w.Body)
	}
	var res struct{ Accepted, Jobs, Anomalies int }
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.Accepted != 30 || res.Jobs != 1 {
		t.Fatalf("logs routing: %+v", res)
	}
	if res.Anomalies == 0 {
		t.Fatal("the new log template should have been flagged")
	}
}

func TestFlushClosesTheOpenBucket(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	do(t, h, http.MethodPost, "/v1/jobs", `{"job":{"name":"f","bucket_span":"1m",
	  "detectors":[{"function":"mean","field":"value","side":"high"}]}}`)

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	for i := 0; i < 60; i++ {
		v := 100.0
		if i%2 == 0 {
			v = 101.0
		}
		if i == 59 { // in the *last*, still-open bucket
			v = 900
		}
		pts = append(pts, core.DataPoint{Time: start.Add(time.Duration(i) * time.Minute), Value: v})
	}
	body, _ := json.Marshal(map[string]any{"points": pts})
	w := do(t, h, http.MethodPost, "/v1/jobs/f/points", string(body))
	var pushed struct {
		Anomalies []core.BucketResult `json:"anomalies"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &pushed)
	if len(pushed.Anomalies) != 0 {
		t.Fatal("the open bucket must not be scored before it is flushed")
	}

	w = do(t, h, http.MethodPost, "/v1/jobs/f/flush", "")
	_ = json.Unmarshal(w.Body.Bytes(), &pushed)
	if len(pushed.Anomalies) == 0 {
		t.Fatal("flush should have closed and scored the final bucket")
	}
}

func TestRegisterJobRejectsBadSpec(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	if w := do(t, h, http.MethodPost, "/v1/jobs", `{"job":{"name":"x"}}`); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a job with no bucket_span/detectors, got %d %s", w.Code, w.Body)
	}
	if w := do(t, h, http.MethodPost, "/v1/jobs", `{"logs":true,"bucket_span":"1m"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a nameless logs job, got %d", w.Code)
	}
}

func TestOTLPBodyLimit(t *testing.T) {
	s := NewServer()
	req := httptest.NewRequest(http.MethodPost, "/v1/otlp/v1/metrics",
		bytes.NewReader(make([]byte, maxBodyBytes+10)))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversized body should be rejected, got %d", w.Code)
	}
}
