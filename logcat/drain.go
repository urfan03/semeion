// Package logcat is semeion's log side: it groups unstructured log messages
// into templates (Drain), then flags NEW, RARE, and SPIKING templates — the
// free equivalent of Elastic ML's categorization jobs, with no license.
//
// Drain (He et al., 2017) is a fixed-depth parse tree: messages are routed
// first by token count, then by their leading tokens, to a leaf holding the
// candidate templates ("log clusters"). It is deterministic and needs no
// training, which keeps detection reproducible.
package logcat

import (
	"regexp"
	"strings"
)

const wildcard = "<*>"

// Cluster is a discovered log template.
type Cluster struct {
	ID     int      `json:"id"`
	Tokens []string `json:"tokens"` // template tokens; wildcard where the position varies
	Count  int      `json:"count"`  // total messages matched
}

// Template renders the cluster's token sequence.
func (c *Cluster) Template() string { return strings.Join(c.Tokens, " ") }

type node struct {
	children map[string]*node
	clusters []*Cluster // populated only at leaves
}

func newNode() *node { return &node{children: make(map[string]*node)} }

// Drain is the parse tree + its clusters. Not safe for concurrent use.
type Drain struct {
	maxDepth int
	simTh    float64
	maxChild int

	root     map[int]*node // keyed by token count
	clusters []*Cluster
	nextID   int
	masks    []mask
}

type mask struct {
	re   *regexp.Regexp
	repl string
}

// NewDrain builds a Drain with sensible defaults (depth 4, similarity 0.4,
// 100 children per node). Common variable patterns are pre-masked to wildcards.
func NewDrain() *Drain {
	return &Drain{
		maxDepth: 4,
		simTh:    0.4,
		maxChild: 100,
		root:     make(map[int]*node),
		masks: []mask{
			{regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`), wildcard}, // UUID
			{regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`), wildcard},                                          // IPv4
			{regexp.MustCompile(`\b0x[0-9a-fA-F]+\b`), wildcard},                                                              // hex
			{regexp.MustCompile(`\b\d+(\.\d+)?\b`), wildcard},                                                                 // numbers
		},
	}
}

// preprocess masks known variable patterns then tokenises on whitespace.
func (d *Drain) preprocess(msg string) []string {
	for _, m := range d.masks {
		msg = m.re.ReplaceAllString(msg, m.repl)
	}
	return strings.Fields(msg)
}

// isVar reports whether a token should be treated as variable for tree routing
// (already a wildcard, or containing a digit).
func isVar(tok string) bool {
	if tok == wildcard {
		return true
	}
	return strings.ContainsAny(tok, "0123456789")
}

// Match returns the cluster for a message, creating or refining templates as
// needed. Returns nil for an empty message.
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

// descend walks the tree to the leaf for tokens. When create is false it stops
// (returns an empty leaf) rather than allocating — used by lookups.
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
			// Cap fan-out: overflow tokens share a wildcard child.
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

// bestMatch picks the leaf cluster most similar to tokens (fraction of exact
// non-wildcard matches), if it clears the similarity threshold.
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

// refine widens the template: positions that now differ become wildcards.
func (d *Drain) refine(c *Cluster, tokens []string) {
	for i := range tokens {
		if c.Tokens[i] != tokens[i] {
			c.Tokens[i] = wildcard
		}
	}
}

// Clusters returns all discovered templates (in creation order).
func (d *Drain) Clusters() []*Cluster { return d.clusters }
