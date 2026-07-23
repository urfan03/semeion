package jobspec

import (
	"testing"
	"time"
)

func TestCalendarCovers(t *testing.T) {
	base := time.Date(2026, 1, 4, 2, 0, 0, 0, time.UTC) // 2026-01-04 is a Sunday

	oneshot := Calendar{Start: base, End: base.Add(time.Hour)}
	if !oneshot.Covers(base.Add(30 * time.Minute)) {
		t.Fatal("one-shot should cover a time inside the window")
	}
	if oneshot.Covers(base.Add(8 * 24 * time.Hour)) {
		t.Fatal("one-shot must not cover a week later")
	}

	daily := Calendar{Start: base, End: base.Add(time.Hour), RecurDaily: true}
	if !daily.Covers(base.Add(24*time.Hour + 30*time.Minute)) {
		t.Fatal("daily should cover 02:30 the next day")
	}
	if daily.Covers(base.Add(24*time.Hour + 2*time.Hour)) {
		t.Fatal("daily must not cover 04:00 (outside 02:00-03:00)")
	}

	weekly := Calendar{Start: base, End: base.Add(time.Hour), RecurWeekly: true}
	if !weekly.Covers(base.Add(7*24*time.Hour + 20*time.Minute)) {
		t.Fatal("weekly should cover the next Sunday 02:20")
	}
	if weekly.Covers(base.Add(24*time.Hour + 20*time.Minute)) {
		t.Fatal("weekly must not cover Monday 02:20")
	}
}
