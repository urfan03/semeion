package correlate

import (
	"testing"
	"time"
)

type clk struct{ t time.Time }

func (c *clk) now() time.Time      { return c.t }
func (c *clk) add(d time.Duration) { c.t = c.t.Add(d) }

func newTestTracker(c *clk) *Tracker {
	tr := NewTracker()
	tr.ResolveAfter = 10 * time.Minute
	tr.Now = c.now
	return tr
}

func inc(id string, start time.Time, maxScore float64, ents ...string) Incident {
	e := map[string]int{}
	for _, x := range ents {
		e[x] = 1
	}
	return Incident{ID: id, Start: start, End: start, MaxScore: maxScore, Entities: e, Jobs: []string{"j"}}
}

func TestTrackerOpensOnceAndKeepsIdentity(t *testing.T) {
	c := &clk{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	tr := newTestTracker(c)

	ev := tr.Reconcile([]Incident{inc("a", c.now(), 80, "service=checkout")})
	if len(ev) != 1 || ev[0].Kind != Opened {
		t.Fatalf("first reconcile should open, got %+v", ev)
	}
	id := ev[0].Incident.ID
	if id == "" {
		t.Fatal("opened incident must get a stable id")
	}

	c.add(time.Minute)
	ev = tr.Reconcile([]Incident{inc("b", c.now(), 82, "service=checkout")})
	if len(ev) != 0 {
		t.Fatalf("a continuing incident must not re-open, got %+v", ev)
	}
	open := tr.Open()
	if len(open) != 1 || open[0].ID != id {
		t.Fatalf("identity should persist across reconciles: %+v", open)
	}
	if open[0].Seen != 2 {
		t.Errorf("Seen should count both passes, got %d", open[0].Seen)
	}
}

func TestTrackerResolvesWhenQuiet(t *testing.T) {
	c := &clk{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	tr := newTestTracker(c)
	tr.Reconcile([]Incident{inc("a", c.now(), 80, "host=web-1")})

	c.add(5 * time.Minute)
	if ev := tr.Reconcile(nil); len(ev) != 0 {
		t.Fatalf("should not resolve before the window, got %+v", ev)
	}

	c.add(6 * time.Minute)
	ev := tr.Reconcile(nil)
	if len(ev) != 1 || ev[0].Kind != Resolved {
		t.Fatalf("should resolve after going quiet, got %+v", ev)
	}
	if len(tr.Open()) != 0 {
		t.Error("resolved incident should leave the open set")
	}
	res := tr.Resolved()
	if len(res) != 1 || res[0].Status != StatusResolved || res[0].ResolvedAt.IsZero() {
		t.Fatalf("resolved history: %+v", res)
	}
}

func TestTrackerFreshActivityDefersResolution(t *testing.T) {
	c := &clk{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	tr := newTestTracker(c)
	tr.Reconcile([]Incident{inc("a", c.now(), 80, "host=web-1")})

	c.add(8 * time.Minute)

	f := inc("a2", c.now(), 80, "host=web-1")
	tr.Reconcile([]Incident{f})

	c.add(8 * time.Minute)
	if ev := tr.Reconcile(nil); len(ev) != 0 {
		t.Fatalf("fresh activity should defer resolution, got %+v", ev)
	}
	if len(tr.Open()) != 1 {
		t.Fatal("incident should still be open")
	}
}

func TestTrackerEscalatesOnBandCrossing(t *testing.T) {
	c := &clk{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	tr := newTestTracker(c)
	tr.Reconcile([]Incident{inc("a", c.now(), 60, "host=web-1")})

	c.add(time.Minute)
	ev := tr.Reconcile([]Incident{inc("a", c.now(), 90, "host=web-1")})
	if len(ev) != 1 || ev[0].Kind != Escalated {
		t.Fatalf("crossing into critical should escalate, got %+v", ev)
	}
	if ev[0].Incident.PeakScore != 90 {
		t.Errorf("peak score should track the max, got %v", ev[0].Incident.PeakScore)
	}

	c.add(time.Minute)
	if ev := tr.Reconcile([]Incident{inc("a", c.now(), 95, "host=web-1")}); len(ev) != 0 {
		t.Fatalf("no second escalation within the band, got %+v", ev)
	}
}

func TestTrackerDistinguishesUnrelatedIncidents(t *testing.T) {
	c := &clk{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	tr := newTestTracker(c)
	ev := tr.Reconcile([]Incident{
		inc("a", c.now(), 80, "host=web-1"),
		inc("b", c.now(), 80, "host=web-2"),
	})
	if len(ev) != 2 {
		t.Fatalf("two disjoint incidents should both open, got %d", len(ev))
	}
	if len(tr.Open()) != 2 {
		t.Fatalf("expected 2 distinct open incidents, got %d", len(tr.Open()))
	}
	ids := map[string]bool{}
	for _, o := range tr.Open() {
		ids[o.ID] = true
	}
	if len(ids) != 2 {
		t.Fatal("the two incidents must have distinct ids")
	}
}

func TestTrackerMatchesByOverlapAsIncidentGrows(t *testing.T) {
	c := &clk{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	tr := newTestTracker(c)
	tr.Reconcile([]Incident{inc("a", c.now(), 80, "service=checkout", "service=cart")})

	c.add(time.Minute)

	ev := tr.Reconcile([]Incident{inc("a2", c.now(), 85, "service=checkout", "service=cart", "service=payments")})
	if len(ev) != 0 {
		t.Fatalf("a growing incident should match, not re-open: %+v", ev)
	}
	if len(tr.Open()) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(tr.Open()))
	}
}

func TestTrackerDoesNotFlapAsIncidentSpreads(t *testing.T) {
	c := &clk{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	tr := newTestTracker(c)

	ev := tr.Reconcile([]Incident{inc("a", c.now(), 90, "host=web-1", "host=web-2")})
	id := ev[0].Incident.ID

	c.add(time.Minute)

	ev = tr.Reconcile([]Incident{inc("a2", c.now(), 90, "host=web-2", "host=web-3", "host=web-4")})
	if len(ev) != 0 {
		t.Fatalf("a spreading incident must not open a new one, got %+v", ev)
	}
	open := tr.Open()
	if len(open) != 1 || open[0].ID != id {
		t.Fatalf("incident identity should persist as it spreads: %+v", open)
	}
}
