package topology

import (
	"sort"
	"sync"
	"time"

	"github.com/urfan03/semeion/otlp"
)

type Edge struct {
	Caller string    `json:"caller"`
	Callee string    `json:"callee"`
	Calls  int       `json:"calls"`
	Errors int       `json:"errors"`
	P50Ms  float64   `json:"p50_ms"`
	LastAt time.Time `json:"last_at"`

	durations []float64
}

type Graph struct {
	mu    sync.RWMutex
	edges map[string]*Edge
	nodes map[string]*Node

	MaxSamples int

	spans    map[string]spanMeta
	orphans  map[string][]spanMeta
	spanFIFO []string
	MaxSpans int
}

type spanMeta struct {
	service string
	err     bool
	start   time.Time
	durMs   float64
}

type Node struct {
	Name   string    `json:"name"`
	Spans  int       `json:"spans"`
	Errors int       `json:"errors"`
	LastAt time.Time `json:"last_at"`
}

func New() *Graph {
	return &Graph{
		edges: map[string]*Edge{}, nodes: map[string]*Node{}, MaxSamples: 1000,
		spans: map[string]spanMeta{}, orphans: map[string][]spanMeta{}, MaxSpans: 200_000,
	}
}

type Snapshot struct {
	Edges []EdgeSnap `json:"edges"`
	Nodes []Node     `json:"nodes"`
}

type EdgeSnap struct {
	Edge
	Durations []float64 `json:"durations,omitempty"`
}

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
			g.addEdge(parent.service, self)
		} else {

			g.orphans[pid] = append(g.orphans[pid], self)
			g.trimOrphans()
		}
	}
}

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

func (g *Graph) trimOrphans() {
	max := g.MaxSpans
	if max <= 0 {
		max = 200_000
	}
	if len(g.orphans) <= max {
		return
	}

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

func (g *Graph) Empty() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.nodes) == 0
}

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

func (g *Graph) UpstreamOf(service string, others []string, maxDepth int) int {
	n := 0
	for _, o := range others {

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
