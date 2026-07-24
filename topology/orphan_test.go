package topology

import (
	"fmt"
	"testing"
	"time"

	"github.com/urfan03/semeion/otlp"
)

func TestOrphanCountBounded(t *testing.T) {
	g := New()
	g.MaxSpans = 100
	base := time.Unix(0, 0).UTC()
	for i := 0; i < 5000; i++ {
		g.Observe([]otlp.Span{{
			TraceID: "T", SpanID: fmt.Sprintf("s%d", i), ParentID: "P",
			Service: "svc", Start: base,
		}})
	}
	if g.orphanCount > g.MaxSpans {
		t.Fatalf("orphan entries must stay bounded by MaxSpans; got orphanCount=%d, MaxSpans=%d", g.orphanCount, g.MaxSpans)
	}
	total := 0
	for _, v := range g.orphans {
		total += len(v)
	}
	if total != g.orphanCount {
		t.Fatalf("orphanCount (%d) must equal the true total orphan entries (%d)", g.orphanCount, total)
	}
}
