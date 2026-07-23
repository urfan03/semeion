package benchmark

import (
	"strings"
	"testing"
	"time"
)

func TestNABScoringMonotonicity(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	win := []AnomalyWindow{
		{Start: t0.Add(10 * time.Minute), End: t0.Add(20 * time.Minute)},
		{Start: t0.Add(50 * time.Minute), End: t0.Add(60 * time.Minute)},
	}
	prof := StandardProfile()

	// Null detector: nothing flagged → normalized 0.
	null := NABNormalized(nil, win, prof)
	if null.Normalized != 0 || null.FN != 2 {
		t.Fatalf("null detector should score 0 with 2 misses, got %+v", null)
	}

	// Perfect detector: flag the front of each window → normalized 100.
	perfect := NABNormalized([]time.Time{win[0].Start, win[1].Start}, win, prof)
	if perfect.Normalized < 99.9 {
		t.Fatalf("front-of-window detection should score ~100, got %v", perfect.Normalized)
	}
	if perfect.TP != 2 || perfect.FP != 0 {
		t.Fatalf("perfect detector counts wrong: %+v", perfect)
	}

	// Early beats late inside the same window.
	early := NABNormalized([]time.Time{win[0].Start.Add(time.Minute), win[1].Start}, win, prof)
	late := NABNormalized([]time.Time{win[0].End.Add(-time.Minute), win[1].Start}, win, prof)
	if early.Normalized <= late.Normalized {
		t.Fatalf("earlier detection should score higher: early=%v late=%v", early.Normalized, late.Normalized)
	}

	// A false positive far from any window lowers the score below perfect.
	withFP := NABNormalized([]time.Time{win[0].Start, win[1].Start, t0.Add(35 * time.Minute)}, win, prof)
	if withFP.FP != 1 {
		t.Fatalf("expected 1 false positive, got %+v", withFP)
	}
	if withFP.Normalized >= perfect.Normalized {
		t.Fatalf("a false positive must lower the score: withFP=%v perfect=%v", withFP.Normalized, perfect.Normalized)
	}

	// reward_low_FP penalizes that same FP harder than standard.
	fpStd := NABNormalized([]time.Time{win[0].Start, win[1].Start, t0.Add(35 * time.Minute)}, win, StandardProfile())
	fpLow := NABNormalized([]time.Time{win[0].Start, win[1].Start, t0.Add(35 * time.Minute)}, win, LowFPProfile())
	if fpLow.Normalized >= fpStd.Normalized {
		t.Fatalf("reward_low_FP should penalize the FP more: low=%v std=%v", fpLow.Normalized, fpStd.Normalized)
	}
}

func TestNABLoaders(t *testing.T) {
	csv := "timestamp,value\n2014-04-01 00:00:00,10.0\n2014-04-01 00:05:00,12.5\n"
	pts, err := LoadNABCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 2 || pts[1].Value != 12.5 {
		t.Fatalf("NAB CSV parse wrong: %+v", pts)
	}
	windows := `[["2014-04-01 00:02:00","2014-04-01 00:08:00"]]`
	ws, err := ParseNABWindows(strings.NewReader(windows))
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 || !ws[0].Start.Equal(time.Date(2014, 4, 1, 0, 2, 0, 0, time.UTC)) {
		t.Fatalf("NAB window parse wrong: %+v", ws)
	}
}
