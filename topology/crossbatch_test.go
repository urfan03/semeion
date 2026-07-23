package topology

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/otlp"
)

func sp(trace, id, parent, svc string, ms int) otlp.Span {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return otlp.Span{TraceID: trace, SpanID: id, ParentID: parent, Service: svc,
		Start: t0, End: t0.Add(time.Duration(ms) * time.Millisecond)}
}

func TestEdgeFormsAcrossBatches(t *testing.T) {
	g := New()
	g.Observe([]otlp.Span{sp("t1", "a", "", "gateway", 300)})
	g.Observe([]otlp.Span{sp("t1", "b", "a", "checkout", 260)})
	if !g.Related("gateway", "checkout") {
		t.Fatalf("cross-batch edge not formed: %+v", g.Edges())
	}
}

func TestEdgeFormsWhenChildArrivesBeforeParent(t *testing.T) {
	g := New()
	g.Observe([]otlp.Span{sp("t2", "c", "b", "payments-db", 200)})
	if len(g.Edges()) != 0 {
		t.Fatalf("no edge should exist before the parent arrives: %+v", g.Edges())
	}
	g.Observe([]otlp.Span{sp("t2", "b", "", "checkout", 260)})
	if !g.Related("checkout", "payments-db") {
		t.Fatalf("orphan child not resolved when parent arrived: %+v", g.Edges())
	}
}

func TestUnresolvedOrphanYieldsNoEdge(t *testing.T) {
	g := New()
	g.Observe([]otlp.Span{sp("t3", "z2", "z1", "checkout", 100)})
	if len(g.Edges()) != 0 {
		t.Fatalf("an unresolved parent must not create an edge: %+v", g.Edges())
	}
}

func TestSpanIndexBounded(t *testing.T) {
	g := New()
	g.MaxSpans = 100
	for i := 0; i < 1000; i++ {
		g.Observe([]otlp.Span{sp("flood", string(rune('A'+i%26))+itoa(i), "", "svc", 1)})
	}
	g.mu.RLock()
	n := len(g.spans)
	g.mu.RUnlock()
	if n > 100 {
		t.Fatalf("span index should be bounded to MaxSpans, got %d", n)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
