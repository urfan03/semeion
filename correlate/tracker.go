package correlate

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// Status is where an incident is in its life.
type Status string

const (
	StatusOpen     Status = "open"
	StatusResolved Status = "resolved"
)

// EventKind is why the tracker emitted an event.
type EventKind string

const (
	Opened    EventKind = "opened"    // a new incident appeared
	Escalated EventKind = "escalated" // an open incident crossed into a higher severity band
	Resolved  EventKind = "resolved"  // an open incident went quiet long enough to close
)

// Tracked is an incident with lifecycle state layered on top. The embedded
// Incident is refreshed on every reconcile; the lifecycle fields persist.
type Tracked struct {
	Incident
	Status       Status    `json:"status"`
	OpenedAt     time.Time `json:"opened_at"`
	LastActivity time.Time `json:"last_activity"`
	ResolvedAt   time.Time `json:"resolved_at,omitempty"`
	Seen         int       `json:"seen"`       // reconcile passes it appeared in
	PeakScore    float64   `json:"peak_score"` // highest score ever seen
}

// Event is a lifecycle transition worth alerting on.
type Event struct {
	Kind     EventKind `json:"kind"`
	Incident Tracked   `json:"incident"`
}

// Tracker gives incidents identity over time.
//
// `Correlate` is stateless: called twice on overlapping data it returns two
// fresh sets with no memory that they describe the same ongoing event. The
// tracker closes that gap — it matches each freshly correlated incident to an
// open one by entity overlap, so a persistent incident is opened once (and
// alerted once), grows in place, and resolves when it finally goes quiet.
//
// It is safe for concurrent use; the server reconciles on the ingest path.
type Tracker struct {
	mu       sync.Mutex
	open     map[string]*Tracked
	resolved []*Tracked
	seq      int

	// ResolveAfter closes an incident once no fresh symptom has arrived for this
	// long, measured against Now (default 15m).
	ResolveAfter time.Duration
	// MatchOverlap is the minimum entity Jaccard overlap to treat a fresh
	// incident as the continuation of an open one (default 0.5).
	MatchOverlap float64
	// MaxResolved bounds the retained resolved history (default 500).
	MaxResolved int
	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time
}

// NewTracker returns a tracker with production defaults.
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

// Reconcile folds a freshly correlated set into the tracked state and returns
// the lifecycle events that resulted. The events are the alertable moments —
// they fire once per transition, never once per poll.
func (t *Tracker) Reconcile(fresh []Incident) []Event {
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()

	var events []Event
	used := map[string]bool{}

	// Match each fresh incident to at most one open incident, best overlap first,
	// so a fresh incident can't be stolen by a weaker match.
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
		// Re-check the match: an earlier, stronger pairing may have claimed it.
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

		// New incident.
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

	// Resolve any open incident that had no fresh match and has gone quiet.
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

// bestMatch finds the open incident with the greatest entity overlap above the
// threshold, skipping any already claimed this pass.
func (t *Tracker) bestMatch(f Incident, used map[string]bool) (string, float64) {
	fe := entitySet(f)
	bestKey, best := "", t.matchOverlap()
	for key, o := range t.open {
		if used[key] {
			continue
		}
		if j := jaccard(fe, entitySet(o.Incident)); j >= best {
			// >= so the threshold itself counts; ties broken by key for determinism.
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

// Open returns the currently open incidents, most recently active first.
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

// Resolved returns recently resolved incidents, most recently resolved first.
func (t *Tracker) Resolved() []Tracked {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Tracked, len(t.resolved))
	for i, o := range t.resolved {
		out[len(t.resolved)-1-i] = *o
	}
	return out
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

// entitySet is an incident's identity for matching: its affected entities, or,
// when it has none (e.g. pure-log incidents with no influencers), its jobs — so
// two entity-less incidents on different jobs still stay distinct.
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

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// severityBand buckets a score the way an on-call reads it, so escalation fires
// on a band crossing (warning → critical) rather than on every point of drift.
func severityBand(score float64) int {
	switch {
	case score >= 75:
		return 2 // critical
	case score >= 50:
		return 1 // warning
	default:
		return 0
	}
}
