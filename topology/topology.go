// Package topology reconstructs the service dependency graph from traces.
//
// It exists for one reason: to answer "was this service upstream of the others
// that were failing?". Without that, correlation can only say two things
// happened together; with it, correlation can say which one was able to cause
// the other. The graph is derived from data (spans), never configured by hand —
// a hand-maintained dependency list is wrong within a week.
package topology

import (
	"sort"
	"sync"
	"time"

	"github.com/urfan03/semeion/otlp"
)

// Edge is one directed call relationship: Caller depends on Callee.
type Edge struct {
	Caller string    `json:"caller"`
	Callee string    `json:"callee"`
	Calls  int       `json:"calls"`
	Errors int       `json:"errors"`
	P50Ms  float64   `json:"p50_ms"`
	LastAt time.Time `json:"last_at"`

	durations []float64 // callee-side span durations, ms
}

// Graph is a service dependency graph. Safe for concurrent use: traces arrive
// on the ingest path while incidents are being correlated on another.
type Graph struct {
	mu    sync.RWMutex
	edges map[string]*Edge // "caller\x00callee"
	nodes map[string]*Node
	// MaxSamples bounds the per-edge latency sample used for the median.
	MaxSamples int
}

// Node is a service and its observed traffic.
type Node struct {
	Name   string    `json:"name"`
	Spans  int       `json:"spans"`
	Errors int       `json:"errors"`
	LastAt time.Time `json:"last_at"`
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{edges: map[string]*Edge{}, nodes: map[string]*Node{}, MaxSamples: 1000}
}

// Observe folds a batch of spans into the graph. Parent lookup is per trace, so
// a batch that only carries part of a trace simply contributes fewer edges — it
// never invents one.
func (g *Graph) Observe(spans []otlp.Span) {
	byID := make(map[string]otlp.Span, len(spans))
	for _, s := range spans {
		byID[s.TraceID+"\x00"+s.SpanID] = s
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	for _, s := range spans {
		n := g.nodes[s.Service]
		if n == nil {
			n = &Node{Name: s.Service}
			g.nodes[s.Service] = n
		}
		n.Spans++
		if s.Error {
			n.Errors++
		}
		if s.Start.After(n.LastAt) {
			n.LastAt = s.Start
		}

		if s.ParentID == "" {
			continue
		}
		parent, ok := byID[s.TraceID+"\x00"+s.ParentID]
		if !ok || parent.Service == "" || parent.Service == s.Service {
			// An unresolved parent, or an internal span: no cross-service edge.
			continue
		}
		key := parent.Service + "\x00" + s.Service
		e := g.edges[key]
		if e == nil {
			e = &Edge{Caller: parent.Service, Callee: s.Service}
			g.edges[key] = e
		}
		e.Calls++
		if s.Error {
			e.Errors++
		}
		if s.Start.After(e.LastAt) {
			e.LastAt = s.Start
		}
		if d := s.Duration(); d > 0 && len(e.durations) < g.maxSamples() {
			e.durations = append(e.durations, float64(d)/float64(time.Millisecond))
		}
	}
}

func (g *Graph) maxSamples() int {
	if g.MaxSamples <= 0 {
		return 1000
	}
	return g.MaxSamples
}

// Edges returns a snapshot, busiest first.
func (g *Graph) Edges() []Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Edge, 0, len(g.edges))
	for _, e := range g.edges {
		c := *e
		c.P50Ms = median(e.durations)
		c.durations = nil
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Calls > out[j].Calls })
	return out
}

// Nodes returns a snapshot, busiest first.
func (g *Graph) Nodes() []Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Node, 0, len(g.nodes))
	for _, n := range g.nodes {
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Spans > out[j].Spans })
	return out
}

// Empty reports whether anything has been observed yet.
func (g *Graph) Empty() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.nodes) == 0
}

// Related reports whether two services call each other, in either direction.
// Used for linking symptoms: a caller and its callee failing together is one
// incident, not two.
func (g *Graph) Related(a, b string) bool {
	if a == "" || b == "" || a == b {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ab := g.edges[a+"\x00"+b]
	_, ba := g.edges[b+"\x00"+a]
	return ab || ba
}

// Reaches reports whether `from` can reach `to` by following calls, i.e. whether
// a failure in `to` could surface as a failure in `from`. Depth-limited: beyond a
// few hops "could have caused" stops meaning anything.
func (g *Graph) Reaches(from, to string, maxDepth int) bool {
	if from == "" || to == "" || from == to {
		return false
	}
	if maxDepth <= 0 {
		maxDepth = 4
	}
	g.mu.RLock()
	defer g.mu.RUnlock()

	seen := map[string]bool{from: true}
	frontier := []string{from}
	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		var next []string
		for _, cur := range frontier {
			for _, e := range g.edges {
				if e.Caller != cur || seen[e.Callee] {
					continue
				}
				if e.Callee == to {
					return true
				}
				seen[e.Callee] = true
				next = append(next, e.Callee)
			}
		}
		frontier = next
	}
	return false
}

// UpstreamOf counts how many of `others` this service sits upstream of — how
// many of the affected services a failure here could explain. That count is the
// topological evidence the root-cause ranking uses.
func (g *Graph) UpstreamOf(service string, others []string, maxDepth int) int {
	n := 0
	for _, o := range others {
		// service is upstream of o when o's traffic path reaches service, i.e.
		// o calls (transitively) into service.
		if g.Reaches(o, service, maxDepth) {
			n++
		}
	}
	return n
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}
