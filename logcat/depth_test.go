package logcat

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
)

// #10: a category keeps several DISTINCT example messages (not just one), counts
// its cumulative matches, and is exposed via Categories() with a stable id.
func TestCategorizationDepthExamplesAndCounts(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewCategorizer(time.Minute)

	var lines []core.LogLine
	// One template ("GET /user/<id> took <n>ms"), many distinct concrete messages.
	for b := 0; b < 30; b++ {
		bt := t0.Add(time.Duration(b) * time.Minute)
		for i := 0; i < 5; i++ {
			msg := "GET /user/" + itoa(b*5+i) + " took " + itoa(10+i) + "ms"
			lines = append(lines, core.LogLine{Time: bt, Message: msg})
		}
	}
	c.Run(lines, 50)

	cats := c.Categories()
	if len(cats) == 0 {
		t.Fatal("expected at least one learned category")
	}
	var def *CategoryDefinition
	for i := range cats {
		if cats[i].NumMatches > 0 {
			def = &cats[i]
			break
		}
	}
	if def == nil {
		t.Fatal("no category carried a match count")
	}
	if def.NumMatches != 150 {
		t.Fatalf("num_matches should be 150 (30 buckets × 5), got %d", def.NumMatches)
	}
	if len(def.Examples) != catMaxExamples {
		t.Fatalf("expected %d distinct examples, got %d (%v)", catMaxExamples, len(def.Examples), def.Examples)
	}
	// The examples must be distinct.
	seen := map[string]bool{}
	for _, e := range def.Examples {
		if seen[e] {
			t.Fatalf("examples must be distinct, saw duplicate %q", e)
		}
		seen[e] = true
	}
	if def.Buckets == 0 || def.FirstSeen.IsZero() {
		t.Fatalf("category definition missing bucket/first-seen metadata: %+v", def)
	}
}

// #10: examples + match counts survive Snapshot/Restore.
func TestCategorizationDepthSnapshotRoundTrip(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewCategorizer(time.Minute)
	var lines []core.LogLine
	for b := 0; b < 25; b++ {
		bt := t0.Add(time.Duration(b) * time.Minute)
		lines = append(lines, core.LogLine{Time: bt, Message: "db query " + itoa(b) + " ok"})
	}
	c.Run(lines, 50)
	before := c.Categories()

	restored := RestoreCategorizer(c.Snapshot())
	after := restored.Categories()
	if len(before) != len(after) {
		t.Fatalf("category count changed across snapshot: %d → %d", len(before), len(after))
	}
	for i := range before {
		if before[i].NumMatches != after[i].NumMatches {
			t.Fatalf("num_matches lost across snapshot for cat %d: %d → %d",
				before[i].ID, before[i].NumMatches, after[i].NumMatches)
		}
		if len(before[i].Examples) != len(after[i].Examples) {
			t.Fatalf("examples lost across snapshot for cat %d", before[i].ID)
		}
	}
}

// itoa avoids importing strconv into the test for a single small conversion.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
