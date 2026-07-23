package otlp

import (
	"testing"
)

const metricsSample = `{
  "resourceMetrics": [{
    "resource": {"attributes": [{"key": "service.name", "value": {"stringValue": "checkout"}}]},
    "scopeMetrics": [{
      "metrics": [
        {"name": "http.server.duration", "gauge": {"dataPoints": [
          {"timeUnixNano": "1767225600000000000", "asDouble": 123.4,
           "attributes": [{"key": "host", "value": {"stringValue": "web-1"}}]},
          {"timeUnixNano": "1767225660000000000", "asInt": "150"}
        ]}},
        {"name": "http.server.requests", "sum": {"dataPoints": [
          {"timeUnixNano": "1767225600000000000", "asInt": "42"}
        ]}},
        {"name": "db.latency", "histogram": {"dataPoints": [
          {"timeUnixNano": "1767225600000000000", "count": "4", "sum": 200},
          {"timeUnixNano": "1767225660000000000", "count": "0", "sum": 0}
        ]}},
        {"name": "no.time", "gauge": {"dataPoints": [{"asDouble": 1}]}}
      ]
    }]
  }]
}`

func TestParseMetrics(t *testing.T) {
	pts, err := ParseMetrics([]byte(metricsSample))
	if err != nil {
		t.Fatal(err)
	}

	if len(pts) != 4 {
		t.Fatalf("expected 4 points, got %d: %+v", len(pts), pts)
	}

	byMetric := map[string][]MetricPoint{}
	for _, p := range pts {
		byMetric[p.Metric] = append(byMetric[p.Metric], p)
	}

	g := byMetric["http.server.duration"]
	if len(g) != 2 {
		t.Fatalf("gauge: got %d points", len(g))
	}
	if g[0].Point.Value != 123.4 {
		t.Errorf("asDouble: %v", g[0].Point.Value)
	}
	if g[1].Point.Value != 150 {
		t.Errorf("asInt (string-encoded int64): %v", g[1].Point.Value)
	}
	if g[0].Point.Fields["service.name"] != "checkout" {
		t.Errorf("resource attribute not inherited: %v", g[0].Point.Fields)
	}
	if g[0].Point.Fields["host"] != "web-1" {
		t.Errorf("point attribute missing: %v", g[0].Point.Fields)
	}

	if g[1].Point.Fields["host"] != "" {
		t.Errorf("attributes leaked between points: %v", g[1].Point.Fields)
	}

	if g[1].Point.Fields["service.name"] != "checkout" {
		t.Errorf("resource attribute lost: %v", g[1].Point.Fields)
	}
	if g[0].Point.Values["http.server.duration"] != 123.4 {
		t.Errorf("metric not addressable by name: %v", g[0].Point.Values)
	}

	h := byMetric["db.latency"]
	if len(h) != 1 || h[0].Point.Value != 50 {
		t.Errorf("histogram should score the mean: %+v", h)
	}
	if g[0].Point.Time.Unix() != 1767225600 {
		t.Errorf("time: %v", g[0].Point.Time)
	}
}

const logsSample = `{
  "resourceLogs": [{
    "resource": {"attributes": [{"key": "service.name", "value": {"stringValue": "api"}}]},
    "scopeLogs": [{
      "logRecords": [
        {"timeUnixNano": "1767225600000000000", "severityText": "ERROR",
         "body": {"stringValue": "payment gateway timeout after 30s"},
         "attributes": [{"key": "route", "value": {"stringValue": "/pay"}}]},
        {"observedTimeUnixNano": "1767225660000000000",
         "body": {"stringValue": "cache warmed"}},
        {"timeUnixNano": "1767225720000000000", "body": {}}
      ]
    }]
  }]
}`

func TestParseLogs(t *testing.T) {
	lines, err := ParseLogs([]byte(logsSample))
	if err != nil {
		t.Fatal(err)
	}

	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %+v", len(lines), lines)
	}
	if lines[0].Fields["severity"] != "ERROR" || lines[0].Fields["route"] != "/pay" {
		t.Errorf("fields: %v", lines[0].Fields)
	}
	if lines[0].Fields["service.name"] != "api" {
		t.Errorf("resource attribute not inherited: %v", lines[0].Fields)
	}
	if lines[1].Time.Unix() != 1767225660 {
		t.Errorf("observedTimeUnixNano fallback failed: %v", lines[1].Time)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := ParseMetrics([]byte("not json")); err == nil {
		t.Error("expected a decode error")
	}
	if _, err := ParseLogs([]byte("{")); err == nil {
		t.Error("expected a decode error")
	}
}
