package datafeed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const promSample = `{
  "status": "success",
  "data": {
    "resultType": "matrix",
    "result": [
      {
        "metric": {"instance": "web-1"},
        "values": [[1767225600, "120.5"], [1767225900, "NaN"], [1767226200, "131"]]
      },
      {
        "metric": {"instance": "web-2"},
        "values": [[1767225600, "90"]]
      }
    ]
  }
}`

func TestPromParse(t *testing.T) {
	pts, err := parsePromRange([]byte(promSample))
	if err != nil {
		t.Fatal(err)
	}

	if len(pts) != 3 {
		t.Fatalf("expected 3 points, got %d", len(pts))
	}
	if pts[0].Value != 120.5 || pts[0].Fields["instance"] != "web-1" {
		t.Fatalf("point[0]: %+v", pts[0])
	}
}

func TestPromFetchHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("query") == "" {
			t.Error("missing query param")
		}
		_, _ = w.Write([]byte(promSample))
	}))
	defer srv.Close()

	src := NewPromSource(srv.URL, "up")
	pts, err := src.Fetch(context.Background(),
		time.Unix(1767225600, 0), time.Unix(1767226200, 0), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 3 {
		t.Fatalf("expected 3 points, got %d", len(pts))
	}
}
