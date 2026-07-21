package logcat

import "testing"

// Structurally identical messages (differing only in a variable past the tree's
// prefix depth) collapse to one template with the varying position wildcarded.
// (Drain routes by the leading tokens, so — like real logs — the constant part
// leads and variables come later.)
func TestDrainGroupsSimilar(t *testing.T) {
	d := NewDrain()
	d.Match("task completed successfully in worker alice")
	d.Match("task completed successfully in worker bob")
	c := d.Match("task completed successfully in worker carol")

	if got := len(d.Clusters()); got != 1 {
		t.Fatalf("expected 1 template, got %d: %v", got, templates(d))
	}
	if c.Template() != "task completed successfully in worker <*>" {
		t.Fatalf("template: got %q", c.Template())
	}
	if c.Count != 3 {
		t.Fatalf("count: got %d", c.Count)
	}
}

// Different message shapes produce distinct templates; numbers/IPs are masked.
func TestDrainDistinctAndMasking(t *testing.T) {
	d := NewDrain()
	d.Match("connection accepted from 10.0.0.1 port 5432")
	d.Match("connection accepted from 10.0.0.9 port 6000")
	d.Match("disk usage is 91 percent on volume data")

	if got := len(d.Clusters()); got != 2 {
		t.Fatalf("expected 2 templates, got %d: %v", got, templates(d))
	}
	// The IP + port varied → wildcards; the static words remain.
	conn := d.Match("connection accepted from 172.16.0.4 port 22")
	if conn.Template() != "connection accepted from <*> port <*>" {
		t.Fatalf("masked template: got %q", conn.Template())
	}
}

// A restored Drain keeps its templates and matches new messages to them.
func TestDrainStateRoundTrip(t *testing.T) {
	d := NewDrain()
	d.Match("job 12 finished in 3 s")
	d.Match("job 44 finished in 9 s")
	st := d.Export()

	if len(st.Clusters) != 1 {
		t.Fatalf("expected 1 cluster in state, got %d", len(st.Clusters))
	}
	d2 := LoadState(st)
	if len(d2.Clusters()) != 1 {
		t.Fatalf("restored clusters: got %d", len(d2.Clusters()))
	}
	// Matching a same-shape message must reuse the restored cluster (no new ID).
	c := d2.Match("job 77 finished in 1 s")
	if len(d2.Clusters()) != 1 {
		t.Fatalf("expected reuse, got %d clusters", len(d2.Clusters()))
	}
	if c.ID != st.Clusters[0].ID {
		t.Fatalf("expected restored ID %d, got %d", st.Clusters[0].ID, c.ID)
	}
}

func templates(d *Drain) []string {
	var out []string
	for _, c := range d.Clusters() {
		out = append(out, c.Template())
	}
	return out
}
