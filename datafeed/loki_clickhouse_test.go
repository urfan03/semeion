package datafeed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const lokiSample = `{
  "status": "success",
  "data": {
    "resultType": "streams",
    "result": [
      {
        "stream": {"app": "checkout", "level": "error"},
        "values": [
          ["1767225600000000000", "payment gateway timeout after 30s"],
          ["1767225660000000000", "payment gateway timeout after 45s"]
        ]
      }
    ]
  }
}`

func TestLokiFetchLogs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/loki/api/v1/query_range") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("query") == "" {
			t.Error("missing LogQL query")
		}
		_, _ = w.Write([]byte(lokiSample))
	}))
	defer srv.Close()

	src := NewLokiSource(srv.URL, `{app="checkout"}`)
	lines, err := src.FetchLogs(context.Background(), time.Unix(1767225600, 0), time.Unix(1767225700, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 log lines, got %d", len(lines))
	}
	if lines[0].Fields["app"] != "checkout" {
		t.Fatalf("stream labels should become fields: %+v", lines[0].Fields)
	}
	if !strings.Contains(lines[0].Message, "payment gateway") {
		t.Fatalf("message: %q", lines[0].Message)
	}
}

const chSample = `{
  "meta": [{"name":"time"},{"name":"value"},{"name":"host"}],
  "data": [
    {"time": "2026-01-01 00:00:00", "value": 120.5, "host": "web-1"},
    {"time": "2026-01-01 00:01:00", "value": 131,   "host": "web-2"}
  ],
  "rows": 2
}`

func TestClickHouseFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		if !strings.Contains(string(body), "FORMAT JSON") {
			t.Errorf("query should request FORMAT JSON, got %q", string(body))
		}
		_, _ = w.Write([]byte(chSample))
	}))
	defer srv.Close()

	src := NewClickHouseSource(srv.URL,
		"SELECT toStartOfMinute(ts) AS time, avg(latency) AS value, host FROM m WHERE ts BETWEEN {{start}} AND {{end}}")
	pts, err := src.Fetch(context.Background(), time.Unix(1767225600, 0), time.Unix(1767225700, 0), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 2 {
		t.Fatalf("expected 2 points, got %d", len(pts))
	}
	if pts[0].Value != 120.5 {
		t.Fatalf("value: got %v", pts[0].Value)
	}
	if pts[0].Fields["host"] != "web-1" {
		t.Fatalf("dimension host: got %q", pts[0].Fields["host"])
	}
	if pts[0].Time.IsZero() {
		t.Fatal("time did not parse")
	}
}
