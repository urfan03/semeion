package correlate

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

type Status string

const (
	StatusOpen     Status = "open"
	StatusResolved Status = "resolved"
)

type EventKind string

const (
	Opened    EventKind = "opened"
	Escalated EventKind = "escalated"
	Resolved  EventKind = "resolved"
)

type Tracked struct {
	Incident
	Status       Status    `json:"status"`
	OpenedAt     time.Time `json:"opened_at"`
	LastActivity time.Time `json:"last_activity"`
	ResolvedAt   time.Time `json:"resolved_at,omitempty"`
	Seen         int       `json:"seen"`
	PeakScore    float64   `json:"peak_score"`
}

type Event struct {
	Kind     EventKind `json:"kind"`
	Incident Tracked   `json:"incident"`
}

type Tracker struct {
	mu       sync.Mutex
	open     map[string]*Tracked
	resolved []*Tracked
	seq      int

	ResolveAfter time.Duration

	MatchOverlap float64

	MaxResolved int

	Now func() time.Time
}

func NewTracker() *Tracker {
	return &Tracker{
		open:         map[string]*Tracked{},
		ResolveAfter: 15 * time.Minute,
		MatchOverlap: 0.5,
		MaxResolved:  500,
	}
}

func (t *Tracker) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now().UTC()
}

func (t *Tracker) resolveAfter() time.Duration {
	if t.ResolveAfter <= 0 {
		return 15 * time.Minute
	}
	return t.ResolveAfter
}

func (t *Tracker) matchOverlap() float64 {
	if t.MatchOverlap <= 0 {
		return 0.5
	}
	return t.MatchOverlap
}

func (t *Tracker) Reconcile(fresh []Incident) []Event {
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()

	var events []Event
	used := map[string]bool{}

	type pairing struct {
		fresh Incident
		key   string
		score float64
	}
	pairs := make([]pairing, 0, len(fresh))
	for _, f := range fresh {
		key, score := t.bestMatch(f, used)
		pairs = append(pairs, pairing{f, key, score})
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].score > pairs[j].score })

	for _, p := range pairs {

		key := p.key
		if key == "" || used[key] {
			key, _ = t.bestMatch(p.fresh, used)
		}
		if key != "" && !used[key] {
			used[key] = true
			o := t.open[key]
			prevBand := severityBand(o.PeakScore)
			o.Incident = p.fresh
			if end := p.fresh.End; end.After(o.LastActivity) {
				o.LastActivity = end
			}
			o.Seen++
			if p.fresh.MaxScore > o.PeakScore {
				o.PeakScore = p.fresh.MaxScore
			}
			o.Incident.ID = key
			o.Incident.Status = string(StatusOpen)
			if severityBand(o.PeakScore) > prevBand {
				events = append(events, Event{Kind: Escalated, Incident: *o})
			}
			continue
		}

		t.seq++
		id := fmt.Sprintf("inc-%d-%04d", now.Unix(), t.seq)
		tr := &Tracked{
			Incident:     p.fresh,
			Status:       StatusOpen,
			OpenedAt:     now,
			LastActivity: p.fresh.End,
			Seen:         1,
			PeakScore:    p.fresh.MaxScore,
		}
		tr.Incident.ID = id
		tr.Incident.Status = string(StatusOpen)
		t.open[id] = tr
		used[id] = true
		events = append(events, Event{Kind: Opened, Incident: *tr})
	}

	for key, o := range t.open {
		if used[key] {
			continue
		}
		if now.Sub(o.LastActivity) >= t.resolveAfter() {
			o.Status = StatusResolved
			o.ResolvedAt = now
			o.Incident.Status = string(StatusResolved)
			delete(t.open, key)
			t.resolved = append(t.resolved, o)
			t.trimResolved()
			events = append(events, Event{Kind: Resolved, Incident: *o})
		}
	}
	return events
}

func (t *Tracker) bestMatch(f Incident, used map[string]bool) (string, float64) {
	fe := entitySet(f)
	bestKey, best := "", t.matchOverlap()
	for key, o := range t.open {
		if used[key] {
			continue
		}
		if j := overlapCoefficient(fe, entitySet(o.Incident)); j >= best {

			if j > best || key < bestKey || bestKey == "" {
				best, bestKey = j, key
			}
		}
	}
	if bestKey == "" {
		return "", 0
	}
	return bestKey, best
}

func (t *Tracker) Open() []Tracked {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Tracked, 0, len(t.open))
	for _, o := range t.open {
		out = append(out, *o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActivity.After(out[j].LastActivity) })
	return out
}

func (t *Tracker) Resolved() []Tracked {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Tracked, len(t.resolved))
	for i, o := range t.resolved {
		out[len(t.resolved)-1-i] = *o
	}
	return out
}

type TrackerSnapshot struct {
	Open     []Tracked `json:"open"`
	Resolved []Tracked `json:"resolved"`
	Seq      int       `json:"seq"`
}

func (t *Tracker) Snapshot() TrackerSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := TrackerSnapshot{Seq: t.seq, Open: make([]Tracked, 0, len(t.open))}
	for _, o := range t.open {
		s.Open = append(s.Open, *o)
	}
	for _, r := range t.resolved {
		s.Resolved = append(s.Resolved, *r)
	}
	return s
}

func (t *Tracker) Restore(s TrackerSnapshot) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seq = s.Seq
	t.open = make(map[string]*Tracked, len(s.Open))
	for i := range s.Open {
		o := s.Open[i]
		t.open[o.ID] = &o
	}
	t.resolved = make([]*Tracked, 0, len(s.Resolved))
	for i := range s.Resolved {
		r := s.Resolved[i]
		t.resolved = append(t.resolved, &r)
	}
}

func (t *Tracker) trimResolved() {
	max := t.MaxResolved
	if max <= 0 {
		max = 500
	}
	if len(t.resolved) > max {
		t.resolved = t.resolved[len(t.resolved)-max:]
	}
}

func entitySet(inc Incident) map[string]bool {
	set := map[string]bool{}
	for e := range inc.Entities {
		set[e] = true
	}
	if len(set) == 0 {
		for _, j := range inc.Jobs {
			set["job:"+j] = true
		}
	}
	return set
}

func overlapCoefficient(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	min := len(a)
	if len(b) < min {
		min = len(b)
	}
	return float64(inter) / float64(min)
}

func severityBand(score float64) int {
	switch {
	case score >= 75:
		return 2
	case score >= 50:
		return 1
	default:
		return 0
	}
}
