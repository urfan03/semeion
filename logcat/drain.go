package logcat

import (
	"regexp"
	"strings"
)

const wildcard = "<*>"

type Cluster struct {
	ID     int      `json:"id"`
	Tokens []string `json:"tokens"`
	Count  int      `json:"count"`
}

func (c *Cluster) Template() string { return strings.Join(c.Tokens, " ") }

type node struct {
	children map[string]*node
	clusters []*Cluster
}

func newNode() *node { return &node{children: make(map[string]*node)} }

type Drain struct {
	maxDepth int
	simTh    float64
	maxChild int

	root     map[int]*node
	clusters []*Cluster
	nextID   int
	masks    []mask
}

type mask struct {
	re   *regexp.Regexp
	repl string
}

func NewDrain() *Drain {
	return &Drain{
		maxDepth: 4,
		simTh:    0.4,
		maxChild: 100,
		root:     make(map[int]*node),
		masks: []mask{
			{regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`), wildcard},
			{regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`), wildcard},
			{regexp.MustCompile(`\b0x[0-9a-fA-F]+\b`), wildcard},
			{regexp.MustCompile(`\b\d+(\.\d+)?\b`), wildcard},
		},
	}
}

func (d *Drain) preprocess(msg string) []string {
	for _, m := range d.masks {
		msg = m.re.ReplaceAllString(msg, m.repl)
	}
	return strings.Fields(msg)
}

func isVar(tok string) bool {
	if tok == wildcard {
		return true
	}
	return strings.ContainsAny(tok, "0123456789")
}

func (d *Drain) Match(msg string) *Cluster {
	tokens := d.preprocess(msg)
	if len(tokens) == 0 {
		return nil
	}
	leaf := d.descend(tokens, true)
	if best := d.bestMatch(leaf.clusters, tokens); best != nil {
		d.refine(best, tokens)
		best.Count++
		return best
	}
	d.nextID++
	c := &Cluster{ID: d.nextID, Tokens: append([]string(nil), tokens...), Count: 1}
	leaf.clusters = append(leaf.clusters, c)
	d.clusters = append(d.clusters, c)
	return c
}

func (d *Drain) descend(tokens []string, create bool) *node {
	L := len(tokens)
	cur := d.root[L]
	if cur == nil {
		if !create {
			return newNode()
		}
		cur = newNode()
		d.root[L] = cur
	}
	limit := d.maxDepth
	if L < limit {
		limit = L
	}
	for depth := 0; depth < limit; depth++ {
		key := tokens[depth]
		if isVar(key) {
			key = wildcard
		}
		nxt := cur.children[key]
		if nxt == nil {

			if len(cur.children) >= d.maxChild {
				key = wildcard
				nxt = cur.children[key]
			}
			if nxt == nil {
				if !create {
					return newNode()
				}
				nxt = newNode()
				cur.children[key] = nxt
			}
		}
		cur = nxt
	}
	return cur
}

func (d *Drain) bestMatch(clusters []*Cluster, tokens []string) *Cluster {
	var best *Cluster
	bestSim := -1.0
	for _, c := range clusters {
		if len(c.Tokens) != len(tokens) {
			continue
		}
		sim := seqSim(c.Tokens, tokens)
		if sim > bestSim {
			bestSim, best = sim, c
		}
	}
	if best != nil && bestSim >= d.simTh {
		return best
	}
	return nil
}

func seqSim(tmpl, tokens []string) float64 {
	if len(tokens) == 0 {
		return 0
	}
	match := 0
	for i := range tokens {
		if tmpl[i] != wildcard && tmpl[i] == tokens[i] {
			match++
		}
	}
	return float64(match) / float64(len(tokens))
}

func (d *Drain) refine(c *Cluster, tokens []string) {
	for i := range tokens {
		if c.Tokens[i] != tokens[i] {
			c.Tokens[i] = wildcard
		}
	}
}

func (d *Drain) Clusters() []*Cluster { return d.clusters }
