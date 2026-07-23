package alert

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
)

func rec(t time.Time, series string, score float64) core.Record {
	return core.Record{
		Time: t, Detector: "mean(latency)", Series: series, Score: score,
		Actual: 900, Typical: 120, Probability: 1e-6, Direction: "up",
		Influencers: []core.Influencer{{Field: "host", Value: "web-3"}},
	}
}

func bucket(recs ...core.Record) core.BucketResult {
	return core.BucketResult{Records: recs}
}

type captureSink struct {
	mu   sync.Mutex
	got  []Alert
	fail error
}

func (c *captureSink) Name() string { return "capture" }
func (c *captureSink) Send(_ context.Context, a Alert) error {
	if c.fail != nil {
		return c.fail
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, a)
	return nil
}

func TestNotifierScoreFloor(t *testing.T) {
	cap := &captureSink{}
	n := NewNotifier(cap)
	n.Dedup = 0

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sent, err := n.Notify(context.Background(), "job", []core.BucketResult{
		bucket(rec(t0, "a", 20), rec(t0, "b", 80)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatalf("expected only the score-80 record, sent=%d", sent)
	}
	if cap.got[0].Series != "b" {
		t.Fatalf("wrong record delivered: %+v", cap.got[0])
	}
}

func TestNotifierDedupPerSeries(t *testing.T) {
	cap := &captureSink{}
	n := NewNotifier(cap)
	n.Dedup = 30 * time.Minute

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	results := []core.BucketResult{
		bucket(rec(t0, "web-1", 90)),
		bucket(rec(t0.Add(5*time.Minute), "web-1", 95)),
		bucket(rec(t0.Add(5*time.Minute), "web-2", 95)),
		bucket(rec(t0.Add(40*time.Minute), "web-1", 95)),
	}
	sent, err := n.Notify(context.Background(), "job", results)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 3 {
		t.Fatalf("expected 3 alerts (2×web-1, 1×web-2), got %d", sent)
	}
}

func TestNotifierOneBrokenSinkDoesNotStopTheOthers(t *testing.T) {
	good := &captureSink{}
	bad := &captureSink{fail: io.ErrUnexpectedEOF}
	n := NewNotifier(bad, good)
	n.Dedup = 0

	t0 := time.Now()
	sent, err := n.Notify(context.Background(), "job", []core.BucketResult{bucket(rec(t0, "a", 90))})
	if err == nil {
		t.Fatal("expected the broken sink's error to be reported")
	}
	if sent != 1 || len(good.got) != 1 {
		t.Fatalf("healthy sink should still have received the alert (sent=%d, got=%d)", sent, len(good.got))
	}
}

func TestSlackSink(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}))
	defer srv.Close()

	s := NewSlackSink(srv.URL)
	a := FromRecord("checkout", rec(time.Unix(1767225600, 0), "web-3", 88))
	if err := s.Send(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	att, ok := body["attachments"].([]any)
	if !ok || len(att) != 1 {
		t.Fatalf("no attachment in payload: %v", body)
	}
	first := att[0].(map[string]any)
	if first["color"] != "#d93025" {
		t.Errorf("score 88 should be critical red, got %v", first["color"])
	}
	if !strings.Contains(first["text"].(string), "host=web-3") {
		t.Errorf("influencers missing from the body: %v", first["text"])
	}
}

func TestWebhookSink(t *testing.T) {
	var got Alert
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Token") != "secret" {
			t.Errorf("custom header not sent")
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()

	w := NewWebhookSink(srv.URL)
	w.Headers = map[string]string{"X-Token": "secret"}
	if err := w.Send(context.Background(), FromRecord("job", rec(time.Now(), "web-3", 88))); err != nil {
		t.Fatal(err)
	}
	if got.Score != 88 || got.Detector != "mean(latency)" {
		t.Fatalf("round-trip lost fields: %+v", got)
	}
}

func TestWebhookSinkReportsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer srv.Close()

	err := NewWebhookSink(srv.URL).Send(context.Background(), Alert{Job: "j"})
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("expected a 502 error, got %v", err)
	}
}

func TestAlertmanagerSink(t *testing.T) {
	var payload []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/alerts" {
			t.Errorf("wrong path %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
	}))
	defer srv.Close()

	m := NewAlertmanagerSink(srv.URL)
	m.ExtraLabels = map[string]string{"env": "prod"}
	if err := m.Send(context.Background(), FromRecord("checkout", rec(time.Unix(1767225600, 0), "web-3", 60))); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 1 {
		t.Fatalf("expected one alert, got %d", len(payload))
	}
	labels := payload[0]["labels"].(map[string]any)
	if labels["severity"] != "warning" {
		t.Errorf("score 60 should be a warning, got %v", labels["severity"])
	}
	if labels["env"] != "prod" {
		t.Errorf("extra labels not merged: %v", labels)
	}
	if labels["alertname"] != "SemeionAnomaly" {
		t.Errorf("alertname: %v", labels["alertname"])
	}
	if payload[0]["endsAt"] == payload[0]["startsAt"] {
		t.Error("endsAt should be startsAt + resolve window")
	}
}

func TestStdoutSink(t *testing.T) {
	var b strings.Builder
	s := StdoutSink{W: &b}
	if err := s.Send(context.Background(), FromRecord("job", rec(time.Now(), "web-3", 90))); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "CRITICAL") || !strings.Contains(b.String(), "mean(latency)") {
		t.Fatalf("unexpected output: %q", b.String())
	}
}

func TestSinkErrorRedactsSecretURL(t *testing.T) {

	secret := "https://hooks.slack.com/services/T00/B00/XXXSECRETXXX"
	s := &SlackSink{WebhookURL: secret}
	err := s.Send(context.Background(), Alert{Job: "j", Score: 90})
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if strings.Contains(err.Error(), "XXXSECRETXXX") {
		t.Fatalf("error leaked the webhook secret: %v", err)
	}
}
