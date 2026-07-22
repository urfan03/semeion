// Package correlate turns a stream of independent anomalies into a small number
// of explained incidents.
//
// Detection answers "what is unusual". On a real system that produces dozens of
// simultaneous records — one per service, per metric, per log template — and a
// human still has to work out that they are one event with one cause. This
// package does that grouping, and then ranks which symptom most likely *caused*
// the rest.
//
// It is deliberately deterministic and explainable: every incident lists the
// evidence that linked it, and every root-cause candidate lists the reasons it
// was ranked where it was. No model, no LLM — those can summarise the result,
// but they must not be what produces it.
package correlate

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/urfan03/semeion/core"
)

// Symptom is one anomaly, flattened out of whichever job produced it.
type Symptom struct {
	Job      string            `json:"job"`
	Time     time.Time         `json:"time"`
	Detector string            `json:"detector,omitempty"`
	Series   string            `json:"series,omitempty"`
	Score    float64           `json:"score"`
	Kind     string            `json:"kind,omitempty"`     // metric | log | change | …
	Template string            `json:"template,omitempty"` // for log symptoms
	Entities map[string]string `json:"entities,omitempty"` // host=web-1, service=checkout, …
}

// Change is a deliberate event — a deploy, a config push, a traffic shift.
// Changes are correlated like symptoms but are strong causal candidates: most
// incidents are caused by something a human did, not by physics.
type Change struct {
	Time   time.Time         `json:"time"`
	Name   string            `json:"name"`
	Kind   string            `json:"kind,omitempty"` // deploy | config | traffic | …
	Labels map[string]string `json:"labels,omitempty"`
}

// Options tune the grouping. The zero value is valid.
type Options struct {
	// Window is how far apart two symptoms can be and still belong together
	// when they share an entity (default 10m).
	Window time.Duration
	// CoWindow is the tighter window used when two symptoms share no entity but
	// come from different signals — co-occurrence is itself weak evidence
	// (default Window/2).
	CoWindow time.Duration
	// MinScore drops symptoms below this before grouping (default 0 = keep all).
	MinScore float64
	// Topology, when set, adds the causal direction that time alone cannot
	// give: services that call each other are linked, and a symptom upstream of
	// the other affected services is ranked as the more likely origin.
	Topology Topology
	// EntityKeys names the entity fields that identify a service in the graph
	// (default: "service", "service.name").
	EntityKeys []string
	// MaxDepth limits how far upstream a cause is looked for (default 4).
	MaxDepth int
}

// Topology is the part of a service dependency graph correlation needs. It is
// an interface so this package keeps no dependency on how the graph was built —
// traces today, a service catalogue or eBPF tomorrow.
type Topology interface {
	// Related reports a direct call relationship in either direction.
	Related(a, b string) bool
	// UpstreamOf counts how many of `others` a failure in `service` could reach.
	UpstreamOf(service string, others []string, maxDepth int) int
}

func (o Options) entityKeys() []string {
	if len(o.EntityKeys) > 0 {
		return o.EntityKeys
	}
	return []string{"service", "service.name"}
}

func (o Options) maxDepth() int {
	if o.MaxDepth > 0 {
		return o.MaxDepth
	}
	return 4
}

// service extracts the symptom's service identity, if it has one.
func (o Options) service(s Symptom) string {
	for _, k := range o.entityKeys() {
		if v, ok := s.Entities[k]; ok && v != "" {
			return v
		}
	}
	return ""
}

func (o Options) window() time.Duration {
	if o.Window <= 0 {
		return 10 * time.Minute
	}
	return o.Window
}

func (o Options) coWindow() time.Duration {
	if o.CoWindow > 0 {
		return o.CoWindow
	}
	return o.window() / 2
}

// Candidate is a ranked root-cause hypothesis.
type Candidate struct {
	Symptom    Symptom  `json:"symptom"`
	Change     *Change  `json:"change,omitempty"`
	Confidence float64  `json:"confidence"` // 0..1, relative within the incident
	Reasons    []string `json:"reasons"`
}

// Incident is a group of symptoms that appear to be one event.
type Incident struct {
	ID        string         `json:"id"`
	Status    string         `json:"status,omitempty"` // set by the Tracker; empty for a one-shot correlation
	Start     time.Time      `json:"start"`
	End       time.Time      `json:"end"`
	Symptoms  []Symptom      `json:"symptoms"`
	Changes   []Change       `json:"changes,omitempty"`
	Entities  map[string]int `json:"entities,omitempty"` // "host=web-1" → symptom count
	Jobs      []string       `json:"jobs"`
	Services  []string       `json:"services,omitempty"` // affected services, first seen first
	MaxScore  float64        `json:"max_score"`
	RootCause []Candidate    `json:"root_cause"`
	Summary   string         `json:"summary"`
}

// FromRecords flattens engine output into symptoms. Influencers and the series
// key both become entities — they are what lets two different signals be
// recognised as being about the same thing.
func FromRecords(job string, results []core.BucketResult) []Symptom {
	var out []Symptom
	for _, br := range results {
		for _, r := range br.Records {
			s := Symptom{
				Job: job, Time: r.Time, Detector: r.Detector, Series: r.Series,
				Score: r.Score, Kind: r.Kind, Template: r.Template,
			}
			for _, in := range r.Influencers {
				if s.Entities == nil {
					s.Entities = map[string]string{}
				}
				s.Entities[in.Field] = in.Value
			}
			out = append(out, s)
		}
	}
	return out
}

// Correlate groups symptoms (and changes) into incidents, newest first.
func Correlate(symptoms []Symptom, changes []Change, opt Options) []Incident {
	kept := make([]Symptom, 0, len(symptoms))
	for _, s := range symptoms {
		if s.Score >= opt.MinScore {
			kept = append(kept, s)
		}
	}
	// A change participates in grouping as if it were a symptom: it must be able
	// to pull an incident's start earlier, which is what makes it a candidate.
	// The origin index travels with the item so sorting can't scramble it.
	items := make([]item, 0, len(kept)+len(changes))
	for _, s := range kept {
		items = append(items, item{sym: s, change: -1})
	}
	for i, c := range changes {
		items = append(items, item{
			sym:    Symptom{Job: c.Name, Time: c.Time, Kind: changeKind(c), Entities: c.Labels},
			change: i,
		})
	}
	if len(items) == 0 {
		return nil
	}
	sort.Slice(items, func(i, j int) bool { return items[i].sym.Time.Before(items[j].sym.Time) })

	incidents := make([]Incident, 0)
	for _, g := range group(items, opt) {
		inc := build(g, items, changes, opt)
		// A group of nothing but changes is not an incident — a deploy on its own
		// is a change, and only becomes a candidate once symptoms cluster near it.
		if len(inc.Symptoms) == 0 {
			continue
		}
		incidents = append(incidents, inc)
	}
	sort.Slice(incidents, func(i, j int) bool { return incidents[i].Start.After(incidents[j].Start) })
	return incidents
}

func changeKind(c Change) string {
	if c.Kind == "" {
		return "change"
	}
	return "change:" + c.Kind
}

// ── grouping ─────────────────────────────────────────────────────────────────

// item is a symptom plus, for a change, its index in the caller's slice.
type item struct {
	sym    Symptom
	change int // -1 when this is an ordinary symptom
}

// group performs single-link clustering over the linkage rules. Items are
// time-sorted, so each one only has to be compared backwards until the window
// closes — O(n · w) rather than O(n²).
func group(items []item, opt Options) [][]int {
	uf := newUnionFind(len(items))
	w, cw := opt.window(), opt.coWindow()
	coarse := coarseEntities(items) // entity values too common to link on
	for i := range items {
		for j := i - 1; j >= 0; j-- {
			gap := items[i].sym.Time.Sub(items[j].sym.Time)
			if gap > w {
				break // everything earlier is further away still
			}
			if linked(items[i].sym, items[j].sym, gap, cw, opt, coarse) {
				uf.union(i, j)
			}
		}
	}
	byRoot := map[int][]int{}
	for i := range items {
		r := uf.find(i)
		byRoot[r] = append(byRoot[r], i)
	}
	out := make([][]int, 0, len(byRoot))
	for _, idx := range byRoot {
		out = append(out, idx)
	}
	return out
}

// linked decides whether two symptoms belong to the same incident.
//
// Sharing an entity (same host, same service) is strong evidence and gets the
// full window. Without a shared entity we only link across *different* signals
// and only within the tighter window: two unrelated services degrading at the
// same moment is usually one upstream cause, but two records from the same job
// on different entities are usually just two independent problems.
func linked(a, b Symptom, gap, coWindow time.Duration, opt Options, coarse map[string]bool) bool {
	if sharesEntity(a, b, coarse) {
		return true
	}
	// A caller and its callee failing together is one incident, however far
	// apart in the window — that is exactly the shape of a cascade.
	if opt.Topology != nil {
		if sa, sb := opt.service(a), opt.service(b); sa != "" && sb != "" && opt.Topology.Related(sa, sb) {
			return true
		}
	}
	if gap > coWindow {
		return false
	}
	return a.Job != b.Job
}

// coarseEntityFraction: an entity value carried by more than this share of all
// symptoms is an attribute (env=prod, region=us-east), not an identity, and must
// not link everything into one mega-incident. It is treated like a stopword.
const coarseEntityFraction = 0.5

// coarseEntities returns the set of "key=value" entity tags too common to be a
// meaningful linking key. Below a handful of symptoms every tag is kept (there
// is no crowd to be common within).
func coarseEntities(items []item) map[string]bool {
	n := 0
	freq := map[string]int{}
	for _, it := range items {
		if it.change >= 0 {
			continue // count symptoms only
		}
		n++
		for k, v := range it.sym.Entities {
			freq[k+"="+v]++
		}
	}
	coarse := map[string]bool{}
	if n < 4 {
		return coarse
	}
	limit := int(coarseEntityFraction * float64(n))
	for tag, c := range freq {
		if c > limit {
			coarse[tag] = true
		}
	}
	return coarse
}

func sharesEntity(a, b Symptom, coarse map[string]bool) bool {
	if len(a.Entities) == 0 || len(b.Entities) == 0 {
		return false
	}
	for k, v := range a.Entities {
		if coarse[k+"="+v] {
			continue // an attribute shared by most symptoms is not an identity
		}
		if bv, ok := b.Entities[k]; ok && bv == v {
			return true
		}
	}
	return false
}

// ── incident assembly ────────────────────────────────────────────────────────

func build(idx []int, items []item, changes []Change, opt Options) Incident {
	sort.Ints(idx)
	inc := Incident{
		Start:    items[idx[0]].sym.Time,
		End:      items[idx[len(idx)-1]].sym.Time,
		Entities: map[string]int{},
	}
	jobs := map[string]bool{}
	for _, i := range idx {
		s := items[i].sym
		if ci := items[i].change; ci >= 0 {
			inc.Changes = append(inc.Changes, changes[ci])
		} else {
			inc.Symptoms = append(inc.Symptoms, s)
			jobs[s.Job] = true
		}
		if s.Score > inc.MaxScore {
			inc.MaxScore = s.Score
		}
		for k, v := range s.Entities {
			inc.Entities[k+"="+v]++
		}
	}
	for j := range jobs {
		inc.Jobs = append(inc.Jobs, j)
	}
	sort.Strings(inc.Jobs)

	// The affected services, in first-seen order — the surface a root cause has
	// to explain.
	seen := map[string]bool{}
	for _, i := range idx {
		if svc := opt.service(items[i].sym); svc != "" && !seen[svc] {
			seen[svc] = true
			inc.Services = append(inc.Services, svc)
		}
	}

	inc.ID = fmt.Sprintf("inc-%d-%s", inc.Start.Unix(), strings.Join(inc.Jobs, "+"))
	inc.RootCause = rank(idx, items, changes, inc, opt)
	inc.Summary = summarize(inc)
	return inc
}

// rank scores each member as a root-cause hypothesis.
//
// The weights encode four beliefs. The strongest is *dependency*: a service the
// others call can explain them, and coincidence cannot manufacture a call path.
// It deliberately outweighs earliness, because in a real cascade the upstream
// component is often noticed last — the user-facing metric is the sensitive one.
// Order of first detection is an artifact of detector sensitivity; the call
// graph is not. A deliberate change ranks high for a different reason: it is the
// thing a human can actually revert.
//
//	dependency 0.35 · change 0.30 · earliness 0.25 · severity 0.10 · breadth 0.10
func rank(idx []int, items []item, changes []Change, inc Incident, opt Options) []Candidate {
	if len(idx) == 0 {
		return nil
	}
	span := inc.End.Sub(inc.Start)

	// The onset of the incident = the earliest actual symptom (not counting
	// changes, which may sit before it). A change can only be a *cause* if it
	// precedes this; a change after it is a remediation, not an origin.
	firstSymptom := inc.End
	for _, i := range idx {
		if items[i].change < 0 && items[i].sym.Time.Before(firstSymptom) {
			firstSymptom = items[i].sym.Time
		}
	}

	cands := make([]Candidate, 0, len(idx))
	for _, i := range idx {
		s := items[i].sym
		var reasons []string
		var score float64

		// Earliness: 1.0 at the incident start, 0 at its end.
		early := 1.0
		if span > 0 {
			early = 1 - float64(s.Time.Sub(inc.Start))/float64(span)
		}
		score += 0.25 * early
		switch {
		case s.Time.Equal(inc.Start):
			reasons = append(reasons, "first event of the incident")
		case early > 0.6:
			reasons = append(reasons, fmt.Sprintf("early (%.0fs after the start)", s.Time.Sub(inc.Start).Seconds()))
		}

		// Deliberate change — but only a cause if it PRECEDES the first symptom.
		// A change during the incident is almost always a remediation, not the
		// origin, so it gets no causal credit (and must not be "roll back"-ed).
		if ci := items[i].change; ci >= 0 {
			if !changes[ci].Time.After(firstSymptom) {
				score += 0.30
				reasons = append(reasons, "deliberate change preceding the incident ("+changes[ci].Name+")")
			} else {
				reasons = append(reasons, "change during the incident ("+changes[ci].Name+"), after symptoms began — not a likely cause")
			}
		}

		// Topological position: how many of the other affected services depend
		// on this one. Coincidence cannot produce this evidence — only an
		// actual call path can.
		if opt.Topology != nil && len(inc.Services) > 1 {
			if svc := opt.service(s); svc != "" {
				others := without(inc.Services, svc)
				if up := opt.Topology.UpstreamOf(svc, others, opt.maxDepth()); up > 0 {
					score += 0.35 * (float64(up) / float64(len(others)))
					reasons = append(reasons, fmt.Sprintf("upstream of %d of the %d other affected service(s)", up, len(others)))
				}
			}
		}

		// Severity, relative to the incident.
		if inc.MaxScore > 0 {
			score += 0.10 * (s.Score / inc.MaxScore)
			if s.Score >= inc.MaxScore {
				reasons = append(reasons, fmt.Sprintf("highest score in the incident (%.0f)", s.Score))
			}
		}

		// Breadth: how much of the incident's entity surface this symptom covers.
		if b := breadth(s, inc); b > 0 {
			score += 0.10 * b
			if b > 0.5 {
				reasons = append(reasons, "touches most of the affected entities")
			}
		}

		if s.Kind == "new" || s.Kind == "rare" {
			reasons = append(reasons, "novel log template")
		}
		if len(reasons) == 0 {
			reasons = append(reasons, "part of the incident")
		}

		c := Candidate{Symptom: s, Confidence: score, Reasons: reasons}
		if ci := items[i].change; ci >= 0 {
			ch := changes[ci]
			c.Change = &ch
		}
		cands = append(cands, c)
	}

	sort.Slice(cands, func(i, j int) bool {
		if cands[i].Confidence != cands[j].Confidence {
			return cands[i].Confidence > cands[j].Confidence
		}
		return cands[i].Symptom.Time.Before(cands[j].Symptom.Time)
	})
	// Confidence = each candidate's SHARE of the total evidence weight, so the
	// leader's number reflects how much it dominates the alternatives: a lone
	// cause reads ~1.0, a genuine coin-flip between two reads ~0.5 each. (The old
	// normalize-to-max pinned the leader to 1.0 always, hiding the margin — the
	// only thing that actually matters — behind fabricated certainty.)
	var total float64
	for _, c := range cands {
		total += c.Confidence
	}
	if total > 0 {
		for i := range cands {
			cands[i].Confidence /= total
		}
	}
	if len(cands) > 5 {
		cands = cands[:5]
	}
	return cands
}

// without returns the list minus one value.
func without(list []string, drop string) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		if v != drop {
			out = append(out, v)
		}
	}
	return out
}

// breadth is the share of the incident's entities this symptom carries.
func breadth(s Symptom, inc Incident) float64 {
	if len(inc.Entities) == 0 || len(s.Entities) == 0 {
		return 0
	}
	hit := 0
	for k, v := range s.Entities {
		if inc.Entities[k+"="+v] > 1 {
			hit++
		}
	}
	return float64(hit) / float64(len(inc.Entities))
}

func summarize(inc Incident) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d symptoms across %d job(s)", len(inc.Symptoms), len(inc.Jobs))
	if len(inc.Changes) > 0 {
		fmt.Fprintf(&b, ", %d change(s)", len(inc.Changes))
	}
	if d := inc.End.Sub(inc.Start); d > 0 {
		fmt.Fprintf(&b, " over %s", d.Round(time.Second))
	}
	if len(inc.RootCause) > 0 {
		c := inc.RootCause[0]
		what := c.Symptom.Job
		if c.Change != nil {
			what = "change " + c.Change.Name
		} else if c.Symptom.Detector != "" {
			what += " / " + c.Symptom.Detector
		}
		fmt.Fprintf(&b, " — likely origin: %s (%s)", what, c.Reasons[0])
	}
	return b.String()
}

// ── union-find ───────────────────────────────────────────────────────────────

type unionFind struct{ parent []int }

func newUnionFind(n int) *unionFind {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	return &unionFind{parent: p}
}

func (u *unionFind) find(i int) int {
	for u.parent[i] != i {
		u.parent[i] = u.parent[u.parent[i]] // path halving
		i = u.parent[i]
	}
	return i
}

func (u *unionFind) union(a, b int) {
	ra, rb := u.find(a), u.find(b)
	if ra != rb {
		u.parent[rb] = ra
	}
}
