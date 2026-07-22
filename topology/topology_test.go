package topology

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/otlp"
)

const traceSample = `{
  "resourceSpans": [
    {
      "resource": {"attributes": [{"key":"service.name","value":{"stringValue":"gateway"}}]},
      "scopeSpans": [{"spans": [
        {"traceId":"t1","spanId":"a1","name":"GET /pay","startTimeUnixNano":"1767225600000000000","endTimeUnixNano":"1767225600300000000"}
      ]}]
    },
    {
      "resource": {"attributes": [{"key":"service.name","value":{"stringValue":"checkout"}}]},
      "scopeSpans": [{"spans": [
        {"traceId":"t1","spanId":"b1","parentSpanId":"a1","name":"charge",
         "startTimeUnixNano":"1767225600050000000","endTimeUnixNano":"1767225600280000000"},
        {"traceId":"t1","spanId":"b2","parentSpanId":"b1","name":"internal.validate",
         "startTimeUnixNano":"1767225600060000000","endTimeUnixNano":"1767225600070000000"}
      ]}]
    },
    {
      "resource": {"attributes": [{"key":"service.name","value":{"stringValue":"payments-db"}}]},
      "scopeSpans": [{"spans": [
        {"traceId":"t1","spanId":"c1","parentSpanId":"b1","name":"SELECT",
         "startTimeUnixNano":"1767225600100000000","endTimeUnixNano":"1767225600250000000",
         "status":{"code":"STATUS_CODE_ERROR"}}
      ]}]
    }
  ]
}`

func build(t *testing.T) *Graph {
	t.Helper()
	spans, err := otlp.ParseTraces([]byte(traceSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 4 {
		t.Fatalf("expected 4 spans, got %d", len(spans))
	}
	g := New()
	g.Observe(spans)
	return g
}

func TestGraphFromTraces(t *testing.T) {
	g := build(t)

	edges := g.Edges()
	// gateway→checkout and checkout→payments-db. The checkout-internal span
	// must NOT create a self-edge.
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d: %+v", len(edges), edges)
	}
	byPair := map[string]Edge{}
	for _, e := range edges {
		byPair[e.Caller+"→"+e.Callee] = e
	}
	if _, ok := byPair["gateway→checkout"]; !ok {
		t.Errorf("missing gateway→checkout: %+v", edges)
	}
	db, ok := byPair["checkout→payments-db"]
	if !ok {
		t.Fatalf("missing checkout→payments-db: %+v", edges)
	}
	if db.Errors != 1 {
		t.Errorf("the errored span should count as an edge error: %+v", db)
	}
	if db.P50Ms != 150 {
		t.Errorf("edge latency should be the callee span duration in ms, got %v", db.P50Ms)
	}
	if len(g.Nodes()) != 3 {
		t.Errorf("expected 3 services, got %d", len(g.Nodes()))
	}
}

func TestRelatedAndReaches(t *testing.T) {
	g := build(t)

	if !g.Related("gateway", "checkout") || !g.Related("checkout", "gateway") {
		t.Error("Related must be direction-agnostic")
	}
	if g.Related("gateway", "payments-db") {
		t.Error("gateway and payments-db are not directly related")
	}
	// Transitively, gateway does reach the database.
	if !g.Reaches("gateway", "payments-db", 4) {
		t.Error("gateway should reach payments-db in 2 hops")
	}
	if g.Reaches("payments-db", "gateway", 4) {
		t.Error("calls are directed — the database does not reach the gateway")
	}
	if g.Reaches("gateway", "payments-db", 1) {
		t.Error("depth 1 must not find a 2-hop path")
	}
}

func TestUpstreamOfCountsWhoDependsOnYou(t *testing.T) {
	g := build(t)
	// Everything depends on the database; the database depends on nothing.
	if n := g.UpstreamOf("payments-db", []string{"gateway", "checkout"}, 4); n != 2 {
		t.Errorf("payments-db should be upstream of both, got %d", n)
	}
	if n := g.UpstreamOf("gateway", []string{"checkout", "payments-db"}, 4); n != 0 {
		t.Errorf("nothing depends on the gateway, got %d", n)
	}
}

func TestObserveIgnoresUnresolvableParents(t *testing.T) {
	// A batch carrying only the child: the parent is in another batch, so no
	// edge may be invented.
	partial := `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},
	  "scopeSpans":[{"spans":[{"traceId":"t9","spanId":"z2","parentSpanId":"z1","name":"charge",
	  "startTimeUnixNano":"1767225600000000000"}]}]}]}`
	spans, err := otlp.ParseTraces([]byte(partial))
	if err != nil {
		t.Fatal(err)
	}
	g := New()
	g.Observe(spans)
	if len(g.Edges()) != 0 {
		t.Fatalf("an unresolved parent must not create an edge: %+v", g.Edges())
	}
	if len(g.Nodes()) != 1 {
		t.Fatalf("the service itself should still be known: %+v", g.Nodes())
	}
}

func TestSpanWithoutAServiceIsDropped(t *testing.T) {
	noSvc := `{"resourceSpans":[{"resource":{"attributes":[]},"scopeSpans":[{"spans":[
	  {"traceId":"t1","spanId":"a1","name":"x","startTimeUnixNano":"1767225600000000000"}]}]}]}`
	spans, err := otlp.ParseTraces([]byte(noSvc))
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 0 {
		t.Fatalf("a span with no service.name cannot be placed in the graph: %+v", spans)
	}
}

func TestEmptyGraph(t *testing.T) {
	g := New()
	if !g.Empty() {
		t.Error("a fresh graph is empty")
	}
	if g.Related("a", "b") || g.Reaches("a", "b", 4) {
		t.Error("an empty graph relates nothing")
	}
	g.Observe([]otlp.Span{{TraceID: "t", SpanID: "s", Service: "a", Start: time.Now()}})
	if g.Empty() {
		t.Error("graph should not be empty after observing a span")
	}
}
