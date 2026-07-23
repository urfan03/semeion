package datafeed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const esSample = `{
  "took": 5,
  "aggregations": {
    "series": {
      "buckets": [
        {"key": 1767225600000, "doc_count": 10, "metric": {"value": 120.5}},
        {"key": 1767225900000, "doc_count": 0,  "metric": {"value": null}},
        {"key": 1767226200000, "doc_count": 7,  "metric": {"value": 131.0}}
      ]
    }
  }
}`

func TestESParseMetric(t *testing.T) {
	pts, err := parseESAgg([]byte(esSample), false)
	if err != nil {
		t.Fatal(err)
	}

	if len(pts) != 2 {
		t.Fatalf("expected 2 points, got %d", len(pts))
	}
	if pts[0].Value != 120.5 {
		t.Fatalf("point[0] value: %v", pts[0].Value)
	}
}

func TestESParseCount(t *testing.T) {
	pts, err := parseESAgg([]byte(esSample), true)
	if err != nil {
		t.Fatal(err)
	}

	if len(pts) != 3 {
		t.Fatalf("expected 3 points, got %d", len(pts))
	}
	if pts[0].Value != 10 || pts[1].Value != 0 {
		t.Fatalf("count values: %v %v", pts[0].Value, pts[1].Value)
	}
}

func TestESFetchHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/_search") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}

		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["aggs"]; !ok {
			t.Error("missing aggs in request body")
		}
		_, _ = w.Write([]byte(esSample))
	}))
	defer srv.Close()

	src := NewESSource(srv.URL, "logs-*", "@timestamp", ESMetric{Func: "mean", Field: "latency"})
	pts, err := src.Fetch(context.Background(),
		time.Unix(1767225600, 0), time.Unix(1767226200, 0), 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 2 {
		t.Fatalf("expected 2 points, got %d", len(pts))
	}
}
