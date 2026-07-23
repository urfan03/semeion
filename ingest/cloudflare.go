package ingest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

// CloudflareMetric is the OTLP-style metric name a live job binds to in order to
// receive Cloudflare HTTP request events. A job created with this metric (or with
// no metric, which accepts everything) is fed the points parsed from Logpush.
const CloudflareMetric = "cloudflare"

// cfEvent is one Cloudflare HTTP-request log line (Logpush / Logpull NDJSON). Only
// the fields semeion turns into dimensions or metrics are decoded; unknown fields
// are ignored. Field names follow Cloudflare's "http_requests" dataset.
type cfEvent struct {
	EdgeStartTimestamp json.RawMessage `json:"EdgeStartTimestamp"` // RFC3339, unix-ns or unix-s

	ClientRequestHost   string `json:"ClientRequestHost"`
	ClientRequestPath   string `json:"ClientRequestPath"`
	ClientRequestMethod string `json:"ClientRequestMethod"`
	ClientCountry       string `json:"ClientCountry"`
	ClientIP            string `json:"ClientIP"`

	EdgeResponseStatus   int `json:"EdgeResponseStatus"`
	OriginResponseStatus int `json:"OriginResponseStatus"`

	EdgeColoCode     string `json:"EdgeColoCode"`
	CacheCacheStatus string `json:"CacheCacheStatus"`
	WAFAction        string `json:"WAFAction"`
	SecurityAction   string `json:"SecurityAction"`

	EdgeResponseBytes        float64      `json:"EdgeResponseBytes"`
	EdgeTimeToFirstByteMs    float64      `json:"EdgeTimeToFirstByteMs"`
	OriginResponseDurationMs float64      `json:"OriginResponseDurationMs"`
	OriginResponseTime       *json.Number `json:"OriginResponseTime"` // legacy: nanoseconds
}

// ParseLogpush reads a Cloudflare Logpush/Logpull NDJSON stream (one JSON object
// per line) and returns one DataPoint per request event. Each point carries the
// request's dimensions in Fields (host, path, method, status, status_class,
// country, colo, cache, waf, client_ip) and its measurements in Values
// (origin_ms, ttfb_ms, resp_bytes, status) so metric, count, rare, population,
// distinct_count and info_content detectors can all run off the same stream.
//
// Malformed lines are skipped (best-effort ingestion of a real log firehose);
// the returned count of skipped lines lets a caller surface a bad feed rather
// than silently under-reporting.
func ParseLogpush(r io.Reader) (points []core.DataPoint, skipped int, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // logs lines can be long
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e cfEvent
		if json.Unmarshal([]byte(line), &e) != nil {
			skipped++
			continue
		}
		ts, ok := parseCFTime(e.EdgeStartTimestamp)
		if !ok {
			skipped++
			continue
		}
		points = append(points, e.point(ts))
	}
	if err := sc.Err(); err != nil {
		return points, skipped, fmt.Errorf("cloudflare logpush: %w", err)
	}
	return points, skipped, nil
}

// LogpushLines projects the same events to LogLines for a categorization job: the
// message is a normalized request signature ("GET api.example.com/user/:id 200"),
// so Drain groups requests into templates and flags new / rare / spiking ones.
func LogpushLines(points []core.DataPoint) []core.LogLine {
	lines := make([]core.LogLine, 0, len(points))
	for _, p := range points {
		f := p.Fields
		msg := fmt.Sprintf("%s %s%s %s", f["method"], f["host"], f["path"], f["status"])
		lines = append(lines, core.LogLine{Time: p.Time, Message: msg, Fields: f})
	}
	return lines
}

func (e cfEvent) point(ts time.Time) core.DataPoint {
	status := e.EdgeResponseStatus
	if status == 0 {
		status = e.OriginResponseStatus
	}
	fields := map[string]string{
		"host":         e.ClientRequestHost,
		"path":         normalizePath(e.ClientRequestPath),
		"method":       e.ClientRequestMethod,
		"status":       strconv.Itoa(status),
		"status_class": statusClass(status),
		"country":      e.ClientCountry,
		"colo":         e.EdgeColoCode,
		"cache":        e.CacheCacheStatus,
		"client_ip":    e.ClientIP,
	}
	if waf := firstNonEmpty(e.WAFAction, e.SecurityAction); waf != "" {
		fields["waf"] = waf
	}
	originMs := e.OriginResponseDurationMs
	if originMs == 0 && e.OriginResponseTime != nil {
		if ns, err := e.OriginResponseTime.Float64(); err == nil {
			originMs = ns / 1e6 // legacy nanoseconds → ms
		}
	}
	values := map[string]float64{
		"status":     float64(status),
		"resp_bytes": e.EdgeResponseBytes,
		"ttfb_ms":    e.EdgeTimeToFirstByteMs,
		"origin_ms":  originMs,
	}
	return core.DataPoint{Time: ts, Value: 1, Fields: fields, Values: values}
}

// parseCFTime accepts EdgeStartTimestamp as an RFC3339 string, a unix-nanosecond
// integer, or a unix-second integer (Cloudflare's three configurable formats).
func parseCFTime(raw json.RawMessage) (time.Time, bool) {
	if len(raw) == 0 {
		return time.Time{}, false
	}
	// String form (quoted): RFC3339 / RFC3339Nano.
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return time.Time{}, false
		}
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t.UTC(), true
		}
		// Some exporters quote the epoch integer.
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return epochToTime(n), true
		}
		return time.Time{}, false
	}
	// Numeric form: nanoseconds or seconds since the epoch.
	n, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return epochToTime(n), true
}

// epochToTime interprets an integer as ns if it's large enough to be a
// nanosecond timestamp (>= ~1e17, i.e. year 1973 in ns), else as seconds.
func epochToTime(n int64) time.Time {
	if n >= 1e17 {
		return time.Unix(0, n).UTC()
	}
	if n >= 1e14 { // milliseconds
		return time.Unix(0, n*1e6).UTC()
	}
	return time.Unix(n, 0).UTC()
}

// statusClass buckets an HTTP status into 2xx/3xx/4xx/5xx (or "other").
func statusClass(status int) string {
	switch {
	case status >= 200 && status < 300:
		return "2xx"
	case status >= 300 && status < 400:
		return "3xx"
	case status >= 400 && status < 500:
		return "4xx"
	case status >= 500 && status < 600:
		return "5xx"
	default:
		return "other"
	}
}

// normalizePath collapses high-cardinality path segments (numeric ids, uuids,
// long hex) to ":id" and drops the query string, so /user/8412/orders and
// /user/93/orders share one template. Keeps the path usable as a dimension.
func normalizePath(p string) string {
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	if p == "" {
		return "/"
	}
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if isVariableSegment(s) {
			segs[i] = ":id"
		}
	}
	return strings.Join(segs, "/")
}

func isVariableSegment(s string) bool {
	if s == "" {
		return false
	}
	digits, hex := 0, 0
	for _, r := range s {
		if unicode.IsDigit(r) {
			digits++
		}
		if unicode.IsDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') || r == '-' {
			hex++
		}
	}
	n := len(s)
	// Mostly-numeric, or a long all-hex/uuid-ish token.
	return digits*2 >= n || (n >= 16 && hex == n)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// CloudflareJob returns a ready-made anomaly-detection job tuned for Cloudflare
// HTTP request logs: per-host traffic level, error-class spikes, origin latency,
// distinct-client fan-out (scraping / DDoS), unusual countries, and path
// diversity. Callers can trim or extend the detector set.
func CloudflareJob(name string, span time.Duration) jobspec.Job {
	high := jobspec.SideHigh
	return jobspec.Job{
		Name:       name,
		BucketSpan: span,
		Detectors: []jobspec.Detector{
			{Function: jobspec.FuncCount, ByField: "host"},                                         // traffic level per host (spike or drop)
			{Function: jobspec.FuncCount, ByField: "status_class", Side: high},                     // error-class spike (5xx/4xx)
			{Function: jobspec.FuncMean, Field: "origin_ms", ByField: "host", Side: high},          // origin latency per host
			{Function: jobspec.FuncDistinctCount, Field: "client_ip", ByField: "host", Side: high}, // client fan-out
			{Function: jobspec.FuncRare, ByField: "country"},                                       // requests from an unusual country
			{Function: jobspec.FuncInfoContent, ByField: "path", PartitionField: "host"},           // path diversity (scanning)
		},
		Influencers: []string{"host", "status_class", "country", "path"},
	}
}
