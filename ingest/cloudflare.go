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

const CloudflareMetric = "cloudflare"

type cfEvent struct {
	EdgeStartTimestamp json.RawMessage `json:"EdgeStartTimestamp"`

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
	OriginResponseTime       *json.Number `json:"OriginResponseTime"`
}

func ParseLogpush(r io.Reader) (points []core.DataPoint, skipped int, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
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
			originMs = ns / 1e6
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

func parseCFTime(raw json.RawMessage) (time.Time, bool) {
	if len(raw) == 0 {
		return time.Time{}, false
	}

	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return time.Time{}, false
		}
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t.UTC(), true
		}

		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return epochToTime(n), true
		}
		return time.Time{}, false
	}

	n, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return epochToTime(n), true
}

func epochToTime(n int64) time.Time {
	if n >= 1e17 {
		return time.Unix(0, n).UTC()
	}
	if n >= 1e14 {
		return time.Unix(0, n*1e6).UTC()
	}
	return time.Unix(n, 0).UTC()
}

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

	return digits*2 >= n || (n >= 16 && hex == n)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func CloudflareJob(name string, span time.Duration) jobspec.Job {
	high := jobspec.SideHigh
	return jobspec.Job{
		Name:       name,
		BucketSpan: span,
		Detectors: []jobspec.Detector{
			{Function: jobspec.FuncCount, ByField: "host"},
			{Function: jobspec.FuncCount, ByField: "status_class", Side: high},
			{Function: jobspec.FuncMean, Field: "origin_ms", ByField: "host", Side: high},
			{Function: jobspec.FuncDistinctCount, Field: "client_ip", ByField: "host", Side: high},
			{Function: jobspec.FuncRare, ByField: "country"},
			{Function: jobspec.FuncInfoContent, ByField: "path", PartitionField: "host"},
		},
		Influencers: []string{"host", "status_class", "country", "path"},
	}
}
