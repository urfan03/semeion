package logcat

import (
	"sort"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
)

func build(lines []core.LogLine, start time.Time, i, n int, msg string) []core.LogLine {
	t := start.Add(time.Duration(i) * time.Minute)
	for k := 0; k < n; k++ {
		lines = append(lines, core.LogLine{Time: t.Add(time.Duration(k) * time.Second), Message: msg})
	}
	return lines
}

func categorizeSeries(t *testing.T) ([]core.BucketResult, *Categorizer) {
	t.Helper()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var lines []core.LogLine

	for i := 0; i < 32; i++ {
		lines = build(lines, start, i, 3, "GET /api/users status 200 in 12 ms")
		lines = build(lines, start, i, 2, "cache hit for key session-42")
	}

	lines = build(lines, start, 30, 5, "panic runtime error nil pointer at 0x1a2b3c4d")

	lines = build(lines, start, 31, 60, "GET /api/users status 200 in 15 ms")

	c := NewCategorizer(time.Minute)
	return c.Run(lines, 50), c
}

func TestCategorizerNewAndSpike(t *testing.T) {
	results, _ := categorizeSeries(t)

	var gotNew, gotSpike bool
	for _, br := range results {
		for _, r := range br.Records {
			switch r.Kind {
			case "new_category":
				if wantContains(r.Template, "panic") {
					gotNew = true
				}
			case "category_spike":
				if wantContains(r.Template, "GET") {
					gotSpike = true
				}
			}
		}
	}
	if !gotNew {
		t.Fatal("expected a new_category record for the panic template")
	}
	if !gotSpike {
		t.Fatal("expected a category_spike record for the GET template")
	}
}

func TestCategorizerSuppress(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var lines []core.LogLine
	for i := 0; i < 25; i++ {
		lines = build(lines, start, i, 3, "GET /api/users status 200")
	}
	lines = build(lines, start, 25, 5, "brand new error code 500 occurred")

	c := NewCategorizer(time.Minute)

	c.Suppress(2)
	results := c.Run(lines, 50)

	for _, br := range results {
		for _, r := range br.Records {
			if r.Series == "T2" {
				t.Fatalf("suppressed template T2 was reported: %+v", r)
			}
		}
	}
}

func TestCategorizerStreamingMatchesBatch(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var lines []core.LogLine
	for i := 0; i < 32; i++ {
		lines = build(lines, start, i, 3, "GET /api/users status 200 in 12 ms")
	}
	lines = build(lines, start, 30, 5, "panic runtime error nil pointer at 0x1a2b3c4d")
	lines = build(lines, start, 31, 60, "GET /api/users status 200 in 15 ms")
	sortLines(lines)

	batch := flattenLog(NewCategorizer(time.Minute).Run(lines, 50))

	stream := NewCategorizer(time.Minute)
	var got []core.BucketResult
	for _, l := range lines {
		got = append(got, stream.Push(l, 50)...)
	}
	got = append(got, stream.Flush(50)...)
	streamed := flattenLog(got)

	if len(batch) != len(streamed) {
		t.Fatalf("record count: batch=%d stream=%d", len(batch), len(streamed))
	}
	if len(batch) == 0 {
		t.Fatal("expected anomalies")
	}
}

func TestCategorizerSnapshotRestore(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var warm []core.LogLine
	for i := 0; i < 30; i++ {
		warm = build(warm, start, i, 3, "GET /api/users status 200 in 12 ms")
	}
	orig := NewCategorizer(time.Minute)
	orig.Run(warm, 50)
	snap := orig.Snapshot()

	restored := RestoreCategorizer(snap)
	if len(restored.Drain().Clusters()) != len(orig.Drain().Clusters()) {
		t.Fatalf("restored clusters: got %d want %d",
			len(restored.Drain().Clusters()), len(orig.Drain().Clusters()))
	}

	var spike []core.LogLine
	spike = build(spike, start, 30, 60, "GET /api/users status 200 in 9 ms")
	res := restored.Run(spike, 50)

	fired := false
	for _, br := range res {
		for _, r := range br.Records {
			if r.Kind == "category_spike" {
				fired = true
			}
		}
	}
	if !fired {
		t.Fatal("expected a spike after restore (baseline should carry over)")
	}
}

func sortLines(lines []core.LogLine) {
	sort.Slice(lines, func(i, j int) bool { return lines[i].Time.Before(lines[j].Time) })
}

func flattenLog(brs []core.BucketResult) []core.Record {
	var out []core.Record
	for _, br := range brs {
		out = append(out, br.Records...)
	}
	return out
}

func wantContains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
