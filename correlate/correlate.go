package correlate

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/urfan03/semeion/core"
)

type Symptom struct {
	Job      string            `json:"job"`
	Time     time.Time         `json:"time"`
	Detector string            `json:"detector,omitempty"`
	Series   string            `json:"series,omitempty"`
	Score    float64           `json:"score"`
	Kind     string            `json:"kind,omitempty"`
	Template string            `json:"template,omitempty"`
	Entities map[string]string `json:"entities,omitempty"`
}

type Change struct {
	Time   time.Time         `json:"time"`
	Name   string            `json:"name"`
	Kind   string            `json:"kind,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

type Options struct {
	Window time.Duration

	CoWindow time.Duration

	MinScore float64

	Topology Topology

	EntityKeys []string

	MaxDepth int
}

type Topology interface {
	Related(a, b string) bool

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

type Candidate struct {
	Symptom    Symptom  `json:"symptom"`
	Change     *Change  `json:"change,omitempty"`
	Confidence float64  `json:"confidence"`
	Reasons    []string `json:"reasons"`
}

type Incident struct {
	ID        string         `json:"id"`
	Status    string         `json:"status,omitempty"`
	Start     time.Time      `json:"start"`
	End       time.Time      `json:"end"`
	Symptoms  []Symptom      `json:"symptoms"`
	Changes   []Change       `json:"changes,omitempty"`
	Entities  map[string]int `json:"entities,omitempty"`
	Jobs      []string       `json:"jobs"`
	Services  []string       `json:"services,omitempty"`
	MaxScore  float64        `json:"max_score"`
	RootCause []Candidate    `json:"root_cause"`
	Summary   string         `json:"summary"`
}

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

func Correlate(symptoms []Symptom, changes []Change, opt Options) []Incident {
	kept := make([]Symptom, 0, len(symptoms))
	for _, s := range symptoms {
		if s.Score >= opt.MinScore {
			kept = append(kept, s)
		}
	}

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

type item struct {
	sym    Symptom
	change int
}

func group(items []item, opt Options) [][]int {
	uf := newUnionFind(len(items))
	w, cw := opt.window(), opt.coWindow()
	coarse := coarseEntities(items)
	for i := range items {
		for j := i - 1; j >= 0; j-- {
			gap := items[i].sym.Time.Sub(items[j].sym.Time)
			if gap > w {
				break
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

func linked(a, b Symptom, gap, coWindow time.Duration, opt Options, coarse map[string]bool) bool {
	if sharesEntity(a, b, coarse) {
		return true
	}

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

const coarseEntityFraction = 0.5

func coarseEntities(items []item) map[string]bool {
	n := 0
	freq := map[string]int{}
	for _, it := range items {
		if it.change >= 0 {
			continue
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
			continue
		}
		if bv, ok := b.Entities[k]; ok && bv == v {
			return true
		}
	}
	return false
}

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

func rank(idx []int, items []item, changes []Change, inc Incident, opt Options) []Candidate {
	if len(idx) == 0 {
		return nil
	}
	span := inc.End.Sub(inc.Start)

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

		if ci := items[i].change; ci >= 0 {
			if !changes[ci].Time.After(firstSymptom) {
				score += 0.30
				reasons = append(reasons, "deliberate change preceding the incident ("+changes[ci].Name+")")
			} else {
				reasons = append(reasons, "change during the incident ("+changes[ci].Name+"), after symptoms began — not a likely cause")
			}
		}

		if opt.Topology != nil && len(inc.Services) > 1 {
			if svc := opt.service(s); svc != "" {
				others := without(inc.Services, svc)
				if up := opt.Topology.UpstreamOf(svc, others, opt.maxDepth()); up > 0 {
					score += 0.35 * (float64(up) / float64(len(others)))
					reasons = append(reasons, fmt.Sprintf("upstream of %d of the %d other affected service(s)", up, len(others)))
				}
			}
		}

		if inc.MaxScore > 0 {
			score += 0.10 * (s.Score / inc.MaxScore)
			if s.Score >= inc.MaxScore {
				reasons = append(reasons, fmt.Sprintf("highest score in the incident (%.0f)", s.Score))
			}
		}

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

func without(list []string, drop string) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		if v != drop {
			out = append(out, v)
		}
	}
	return out
}

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
		u.parent[i] = u.parent[u.parent[i]]
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
