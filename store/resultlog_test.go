package store

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
)

func TestResultLogAppendQuery(t *testing.T) {
	l := NewResultLog(t.TempDir())
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rec := func(min int, score float64) core.Record {
		return core.Record{Time: t0.Add(time.Duration(min) * time.Minute), Detector: "mean(v)", Series: "host=a", Score: score}
	}
	if err := l.Append("web/latency", []core.BucketResult{
		{Time: t0, Records: []core.Record{rec(0, 80)}},
		{Time: t0.Add(time.Minute), Records: []core.Record{rec(1, 90)}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.Append("web/latency", []core.BucketResult{
		{Time: t0.Add(2 * time.Minute), Records: []core.Record{rec(2, 70)}},
	}); err != nil {
		t.Fatal(err)
	}

	all, err := l.Query("web/latency", time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("appends should accumulate to 3 records, got %d", len(all))
	}

	win, err := l.Query("web/latency", t0.Add(time.Minute), t0.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(win) != 2 {
		t.Fatalf("time-range query should return 2 records, got %d", len(win))
	}
	if win[0].Score != 90 {
		t.Fatalf("first in-window record wrong: %+v", win[0])
	}

	if empty, _ := l.Query("does-not-exist", time.Time{}, time.Time{}); len(empty) != 0 {
		t.Fatalf("querying an unknown job should be empty, got %d", len(empty))
	}
}
