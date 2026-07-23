package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/correlate"
)

const threeTierTrace = `{"resourceSpans":[
 {"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"gateway"}}]},
  "scopeSpans":[{"spans":[{"traceId":"t1","spanId":"a","name":"GET /pay",
   "startTimeUnixNano":"1767225600000000000","endTimeUnixNano":"1767225600300000000"}]}]},
 {"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},
  "scopeSpans":[{"spans":[{"traceId":"t1","spanId":"b","parentSpanId":"a","name":"charge",
   "startTimeUnixNano":"1767225600050000000","endTimeUnixNano":"1767225600280000000"}]}]},
 {"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"payments-db"}}]},
  "scopeSpans":[{"spans":[{"traceId":"t1","spanId":"c","parentSpanId":"b","name":"SELECT",
   "startTimeUnixNano":"1767225600100000000","endTimeUnixNano":"1767225600250000000"}]}]}
]}`

func TestOTLPTracesBuildTheGraph(t *testing.T) {
	s := NewServer()
	h := s.Handler()

	w := do(t, h, http.MethodPost, "/v1/otlp/v1/traces", threeTierTrace)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var res struct{ Spans, Services int }
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.Spans != 3 || res.Services != 3 {
		t.Fatalf("expected 3 spans / 3 services, got %+v", res)
	}

	w = do(t, h, http.MethodGet, "/v1/topology", "")
	var topo struct {
		Nodes []map[string]any `json:"nodes"`
		Edges []map[string]any `json:"edges"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &topo)
	if len(topo.Edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(topo.Edges))
	}
}

func TestIncidentsUseTopologyForRootCause(t *testing.T) {
	s := NewServer()
	h := s.Handler()

	if w := do(t, h, http.MethodPost, "/v1/otlp/v1/traces", threeTierTrace); w.Code != http.StatusOK {
		t.Fatalf("traces: %d %s", w.Code, w.Body)
	}

	base := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	svc := func(job, service string, off time.Duration, score float64) core.BucketResult {
		ts := base.Add(off)
		return core.BucketResult{Time: ts, Records: []core.Record{{
			Time: ts, Detector: "mean(latency)", Series: service, Score: score, Kind: "metric",
			Influencers: []core.Influencer{{Field: "service", Value: service}},
		}}}
	}

	s.Store("gateway-latency", []core.BucketResult{svc("gateway-latency", "gateway", 0, 95)})
	s.Store("checkout-latency", []core.BucketResult{svc("checkout-latency", "checkout", 30*time.Second, 88)})
	s.Store("db-latency", []core.BucketResult{svc("db-latency", "payments-db", time.Minute, 65)})

	w := do(t, h, http.MethodGet, "/v1/incidents?window=10m", "")
	var res struct {
		Incidents []correlate.Incident `json:"incidents"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Incidents) != 1 {
		t.Fatalf("all three tiers should be one incident, got %d", len(res.Incidents))
	}
	top := res.Incidents[0].RootCause[0]
	if top.Symptom.Entities["service"] != "payments-db" {
		t.Fatalf("the upstream database should be blamed, got %q\nreasons: %v",
			top.Symptom.Entities["service"], top.Reasons)
	}
	if !strings.Contains(strings.Join(top.Reasons, "; "), "upstream of") {
		t.Errorf("the topological reason should be cited: %v", top.Reasons)
	}
}

func TestTopologyIgnoredUntilTracesArrive(t *testing.T) {
	s := NewServer()
	h := s.Handler()

	base := time.Now().UTC()
	for i, svc := range []string{"a", "b"} {
		ts := base.Add(time.Duration(i) * time.Second)
		s.Store(fmt.Sprintf("job-%s", svc), []core.BucketResult{{Time: ts, Records: []core.Record{{
			Time: ts, Detector: "d", Score: 90, Kind: "metric",
			Influencers: []core.Influencer{{Field: "service", Value: svc}},
		}}}})
	}
	if w := do(t, h, http.MethodGet, "/v1/incidents", ""); w.Code != http.StatusOK {
		t.Fatalf("incidents should work without traces: %d %s", w.Code, w.Body)
	}
}
