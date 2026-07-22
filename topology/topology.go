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

	// A persistent span index so a parent and its child resolve into an edge
	// even when they arrive in SEPARATE Observe batches — the normal case with
	// per-service OTLP collectors. `spans` maps traceID\x00spanID → the span's
	// service; `orphans` buffers children whose parent hasn't been seen yet,
	// keyed by the parent's traceID\x00spanID. Both are bounded (MaxSpans).
	spans    map[string]spanMeta
	orphans  map[string][]spanMeta
	spanFIFO []string // insertion order for eviction
	MaxSpans int
}

// spanMeta is the part of a span the edge stats need, kept in the index.
type spanMeta struct {
	service string
	err     bool
	start   time.Time
	durMs   float64
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
	return &Graph{
		edges: map[string]*Edge{}, nodes: map[string]*Node{}, MaxSamples: 1000,
		spans: map[string]spanMeta{}, orphans: map[string][]spanMeta{}, MaxSpans: 200_000,
	}
}

// Snapshot is a serializable copy of the graph for persistence.
type Snapshot struct {
	Edges []EdgeSnap `json:"edges"`
	Nodes []Node     `json:"nodes"`
}

// EdgeSnap carries an edge plus its latency samples (which Edge hides).
type EdgeSnap struct {
	Edge
	Durations []float64 `json:"durations,omitempty"`
}

// Snapshot returns a serializable copy of the graph.
func (g *Graph) Snapshot() Snapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()
	s := Snapshot{Edges: make([]EdgeSnap, 0, len(g.edges)), Nodes: make([]Node, 0, len(g.nodes))}
	for _, e := range g.edges {
		s.Edges = append(s.Edges, EdgeSnap{Edge: *e, Durations: append([]float64(nil), e.durations...)})
	}
	for _, n := range g.nodes {
		s.Nodes = append(s.Nodes, *n)
	}
	return s
}

// Restore replaces the graph's contents with a snapshot.
func (g *Graph) Restore(s Snapshot) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.edges = make(map[string]*Edge, len(s.Edges))
	g.nodes = make(map[string]*Node, len(s.Nodes))
	for _, es := range s.Edges {
		e := es.Edge
		e.durations = append([]float64(nil), es.Durations...)
		g.edges[e.Caller+"\x00"+e.Callee] = &e
	}
	for i := range s.Nodes {
		n := s.Nodes[i]
		g.nodes[n.Name] = &n
	}
}

// Observe folds a batch of spans into the graph. Cross-service edges resolve
// through a persistent span index, so a caller and its callee produce an edge
// even when they arrive in different batches (per-service collectors) or out of
// order (child before parent) — without ever inventing an edge.
func (g *Graph) Observe(spans []otlp.Span) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, s := range spans {
		if s.SpanID == "" || s.Service == "" {
			continue
		}
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

		self := spanMeta{service: s.Service, err: s.Error, start: s.Start}
		if d := s.Duration(); d > 0 {
			self.durMs = float64(d) / float64(time.Millisecond)
		}
		id := s.TraceID + "\x00" + s.SpanID
		g.indexSpan(id, self)

		// This span may be the parent some earlier orphan child was waiting for.
		if kids, ok := g.orphans[id]; ok {
			for _, kid := range kids {
				g.addEdge(self.service, kid)
			}
			delete(g.orphans, id)
		}

		if s.ParentID == "" {
			continue
		}
		pid := s.TraceID + "\x00" + s.ParentID
		if parent, ok := g.spans[pid]; ok {
			g.addEdge(parent.service, self) // parent already seen
		} else {
			// Parent not seen yet: buffer this child until it arrives.
			g.orphans[pid] = append(g.orphans[pid], self)
			g.trimOrphans()
		}
	}
}

// addEdge records (or updates) a caller→callee edge using the callee span's
// error/latency. An internal (same-service) span makes no cross-service edge.
func (g *Graph) addEdge(caller string, callee spanMeta) {
	if caller == "" || callee.service == "" || caller == callee.service {
		return
	}
	key := caller + "\x00" + callee.service
	e := g.edges[key]
	if e == nil {
		e = &Edge{Caller: caller, Callee: callee.service}
		g.edges[key] = e
	}
	e.Calls++
	if callee.err {
		e.Errors++
	}
	if callee.start.After(e.LastAt) {
		e.LastAt = callee.start
	}
	if callee.durMs > 0 && len(e.durations) < g.maxSamples() {
		e.durations = append(e.durations, callee.durMs)
	}
}

// indexSpan records a span in the bounded index, evicting the oldest when full.
func (g *Graph) indexSpan(id string, m spanMeta) {
	if _, exists := g.spans[id]; !exists {
		g.spanFIFO = append(g.spanFIFO, id)
	}
	g.spans[id] = m
	max := g.MaxSpans
	if max <= 0 {
		max = 200_000
	}
	for len(g.spanFIFO) > max {
		old := g.spanFIFO[0]
		g.spanFIFO = g.spanFIFO[1:]
		delete(g.spans, old)
	}
}

// trimOrphans bounds the orphan buffer; unresolved children are dropped oldest-
// first (a parent that never arrives simply yields no edge).
func (g *Graph) trimOrphans() {
	max := g.MaxSpans
	if max <= 0 {
		max = 200_000
	}
	if len(g.orphans) <= max {
		return
	}
	// Cheap bound: when far over, clear — orphans are transient resolution state,
	// never a data source, so dropping them only forgoes some edges.
	for k := range g.orphans {
		delete(g.orphans, k)
		if len(g.orphans) <= max*3/4 {
			break
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
