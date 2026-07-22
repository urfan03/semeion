package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"strings"
	"time"
)

const defaultTimeout = 10 * time.Second

func httpClient(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{Timeout: defaultTimeout}
}

// postJSON is the one place any sink talks HTTP.
func postJSON(ctx context.Context, c *http.Client, url string, body any, headers map[string]string) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient(c).Do(req)
	if err != nil {
		// A transport error's message embeds the full request URL, and a Slack /
		// generic webhook URL IS the secret. Redact it so an outage doesn't write
		// the credential to logs on every retry.
		return fmt.Errorf("POST failed: %s", redactURL(err.Error(), url))
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// redactURL replaces occurrences of the (secret-bearing) URL in a message with
// a scheme+host-only form, so error logs never carry the full credential.
func redactURL(msg, full string) string {
	if full == "" {
		return msg
	}
	safe := full
	if u, err := neturl.Parse(full); err == nil && u.Host != "" {
		safe = u.Scheme + "://" + u.Host + "/…"
	}
	return strings.ReplaceAll(msg, full, safe)
}

// ── Slack ────────────────────────────────────────────────────────────────────

// SlackSink posts to an incoming-webhook URL.
type SlackSink struct {
	WebhookURL string
	HTTP       *http.Client
	// LinkBase, when set, turns the job name into a link to your Explorer,
	// e.g. https://semeion.internal → …/#job=<name>.
	LinkBase string
}

func NewSlackSink(webhookURL string) *SlackSink { return &SlackSink{WebhookURL: webhookURL} }

func (s *SlackSink) Name() string { return "slack" }

func (s *SlackSink) Send(ctx context.Context, a Alert) error {
	title := a.Title()
	if s.LinkBase != "" {
		title = fmt.Sprintf("<%s/#job=%s|%s>", strings.TrimRight(s.LinkBase, "/"), a.Job, title)
	}
	payload := map[string]any{
		"text": a.Title(), // notification fallback
		"attachments": []map[string]any{{
			"color":     slackColor(a),
			"title":     title,
			"text":      a.Description(),
			"footer":    "semeion",
			"ts":        a.Time.Unix(),
			"mrkdwn_in": []string{"text", "title"},
		}},
	}
	return postJSON(ctx, s.HTTP, s.WebhookURL, payload, nil)
}

func slackColor(a Alert) string {
	switch a.Severity() {
	case "critical":
		return "#d93025" // red
	case "warning":
		return "#f59e0b" // amber
	default:
		return "#64748b" // slate
	}
}

// ── Generic webhook ──────────────────────────────────────────────────────────

// WebhookSink POSTs the raw alert JSON — the escape hatch for anything we don't
// ship a dedicated sink for.
type WebhookSink struct {
	URL     string
	Headers map[string]string
	HTTP    *http.Client
}

func NewWebhookSink(url string) *WebhookSink { return &WebhookSink{URL: url} }

func (w *WebhookSink) Name() string { return "webhook" }

func (w *WebhookSink) Send(ctx context.Context, a Alert) error {
	a.Influencers = sortedInfluencers(a.Influencers)
	return postJSON(ctx, w.HTTP, w.URL, a, w.Headers)
}

// ── Alertmanager ─────────────────────────────────────────────────────────────

// AlertmanagerSink pushes to Prometheus Alertmanager's v2 API, so anomalies land
// in the same routing / silencing / on-call rules as the rest of your alerts.
type AlertmanagerSink struct {
	BaseURL string // e.g. http://alertmanager:9093
	// Duration the alert stays firing without a refresh (default 3× dedup ≈ 10m).
	Resolve time.Duration
	// ExtraLabels are merged into every alert (env, cluster, team, …).
	ExtraLabels map[string]string
	HTTP        *http.Client
}

func NewAlertmanagerSink(baseURL string) *AlertmanagerSink {
	return &AlertmanagerSink{BaseURL: baseURL, Resolve: 10 * time.Minute}
}

func (m *AlertmanagerSink) Name() string { return "alertmanager" }

func (m *AlertmanagerSink) Send(ctx context.Context, a Alert) error {
	resolve := m.Resolve
	if resolve <= 0 {
		resolve = 10 * time.Minute
	}
	labels := map[string]string{
		"alertname": "SemeionAnomaly",
		"job":       a.Job,
		"detector":  a.Detector,
		"severity":  a.Severity(),
		"source":    "semeion",
	}
	if a.Series != "" {
		labels["series"] = a.Series
	}
	if a.Kind != "" {
		labels["kind"] = a.Kind
	}
	for k, v := range m.ExtraLabels {
		labels[k] = v
	}
	payload := []map[string]any{{
		"labels": labels,
		"annotations": map[string]string{
			"summary":     a.Title(),
			"description": a.Description(),
			"score":       fmt.Sprintf("%.1f", a.Score),
		},
		"startsAt": a.Time.UTC().Format(time.RFC3339),
		"endsAt":   a.Time.Add(resolve).UTC().Format(time.RFC3339),
	}}
	return postJSON(ctx, m.HTTP, strings.TrimRight(m.BaseURL, "/")+"/api/v2/alerts", payload, nil)
}

// ── stdout ───────────────────────────────────────────────────────────────────

// StdoutSink prints alerts — the default for `watch` when no sink is configured,
// so a run is never silently doing nothing.
type StdoutSink struct{ W io.Writer }

func (s StdoutSink) Name() string { return "stdout" }

func (s StdoutSink) Send(_ context.Context, a Alert) error {
	w := s.W
	if w == nil {
		w = os.Stdout
	}
	_, err := fmt.Fprintf(w, "[%s] %s\n  %s\n",
		strings.ToUpper(a.Severity()), a.Title(),
		strings.ReplaceAll(a.Description(), "\n", "\n  "))
	return err
}
