package benchmark

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"time"

	"github.com/urfan03/semeion/core"
)

// This file implements the Numenta Anomaly Benchmark (NAB) scoring model — the
// standard, published way to compare streaming anomaly detectors. Unlike a flat
// windowed F1, NAB rewards EARLY detection inside an anomaly window (a
// scaled-sigmoid weight), discounts false positives that land just after a real
// window, and normalizes to 0..100 against a no-detections null and a
// perfect-early-detection ceiling — under a chosen application profile.
//
// Reference: Lavin & Ahmad, "Evaluating Real-Time Anomaly Detection Algorithms —
// the Numenta Anomaly Benchmark" (2015).

// AnomalyWindow is a ground-truth anomaly interval [Start, End].
type AnomalyWindow struct {
	Start time.Time
	End   time.Time
}

// NABProfile weights the three outcomes. Presets mirror NAB's application
// profiles: Standard, reward-low-FP, reward-low-FN.
type NABProfile struct {
	Name string
	ATP  float64 // reward for a true positive at the front of a window
	AFP  float64 // penalty for a false positive far from any window
	AFN  float64 // penalty for a missed window
}

// The three canonical NAB application profiles.
func StandardProfile() NABProfile { return NABProfile{"standard", 1.0, 0.11, 1.0} }
func LowFPProfile() NABProfile    { return NABProfile{"reward_low_FP", 1.0, 0.22, 1.0} }
func LowFNProfile() NABProfile    { return NABProfile{"reward_low_FN", 1.0, 0.11, 2.0} }

// NABResult is a NAB score under one profile.
type NABResult struct {
	Profile    string  `json:"profile"`
	Raw        float64 `json:"raw"`
	Normalized float64 `json:"normalized"` // 0..100 (100 = perfect early detection, 0 = no better than doing nothing)
	TP         int     `json:"tp"`
	FP         int     `json:"fp"`
	FN         int     `json:"fn"`
}

// scaledSigmoid is NAB's position weight: at the front of a window (y=-1) it is
// ~+1 (full early-detection reward); at the window end (y=0) it is 0; past the
// window (y>0) it goes negative toward -1 (a false positive far from any window).
func scaledSigmoid(y float64) float64 {
	if y > 3 {
		return -1
	}
	return 2/(1+math.Exp(5*y)) - 1
}

// NABScore computes the raw NAB score and TP/FP/FN counts for a set of detection
// timestamps against the anomaly windows under a profile. Only the EARLIEST
// detection inside a window counts (a window is either caught or missed); other
// in-window detections are ignored; detections outside every window are false
// positives weighted by how soon after the preceding window they land.
func NABScore(detections []time.Time, windows []AnomalyWindow, p NABProfile) (raw float64, tp, fp, fn int) {
	dets := append([]time.Time(nil), detections...)
	sort.Slice(dets, func(i, j int) bool { return dets[i].Before(dets[j]) })
	ws := append([]AnomalyWindow(nil), windows...)
	sort.Slice(ws, func(i, j int) bool { return ws[i].Start.Before(ws[j].Start) })

	inWindow := func(t time.Time) int {
		for i, w := range ws {
			if !t.Before(w.Start) && !t.After(w.End) {
				return i
			}
		}
		return -1
	}
	// Earliest detection index per window (or -1).
	firstHit := make([]int, len(ws))
	for i := range firstHit {
		firstHit[i] = -1
	}
	for di, t := range dets {
		if wi := inWindow(t); wi >= 0 && firstHit[wi] == -1 {
			firstHit[wi] = di
		}
	}
	// True positives / misses.
	for wi, w := range ws {
		if firstHit[wi] >= 0 {
			t := dets[firstHit[wi]]
			raw += p.ATP * scaledSigmoid(relPos(t, w))
			tp++
		} else {
			raw -= p.AFN
			fn++
		}
	}
	// False positives: detections in no window, weighted by nearness to the
	// preceding window's end (a late "echo" of a real anomaly is cheap).
	for _, t := range dets {
		if inWindow(t) >= 0 {
			continue
		}
		fp++
		prev := prevWindow(ws, t)
		if prev < 0 {
			raw -= p.AFP // before any window: full penalty
			continue
		}
		w := ws[prev]
		wlen := w.End.Sub(w.Start)
		if wlen <= 0 {
			wlen = time.Second
		}
		y := t.Sub(w.End).Seconds() / wlen.Seconds()
		raw += p.AFP * scaledSigmoid(y) // scaledSigmoid(y>0) < 0 → a penalty
	}
	return raw, tp, fp, fn
}

// NABNormalized runs NABScore and normalizes it to 0..100: 0 = the null detector
// (flags nothing), 100 = the perfect detector (flags the first instant of every
// window). Negative raw scores (worse than doing nothing) clamp at 0.
func NABNormalized(detections []time.Time, windows []AnomalyWindow, p NABProfile) NABResult {
	raw, tp, fp, fn := NABScore(detections, windows, p)
	null, _, _, _ := NABScore(nil, windows, p)
	perfect := make([]time.Time, len(windows))
	for i, w := range windows {
		perfect[i] = w.Start
	}
	best, _, _, _ := NABScore(perfect, windows, p)
	norm := 0.0
	if best > null {
		norm = 100 * (raw - null) / (best - null)
	}
	if norm < 0 {
		norm = 0
	}
	if norm > 100 {
		norm = 100
	}
	return NABResult{Profile: p.Name, Raw: raw, Normalized: norm, TP: tp, FP: fp, FN: fn}
}

// relPos is the detection's position within a window, in [-1, 0]: -1 at the
// front (earliest, best), 0 at the end.
func relPos(t time.Time, w AnomalyWindow) float64 {
	wlen := w.End.Sub(w.Start)
	if wlen <= 0 {
		return 0
	}
	y := t.Sub(w.End).Seconds() / wlen.Seconds() // (t-end)/len ∈ [-1, 0]
	if y < -1 {
		y = -1
	}
	if y > 0 {
		y = 0
	}
	return y
}

// prevWindow returns the index of the latest window ending at or before t, or -1.
func prevWindow(ws []AnomalyWindow, t time.Time) int {
	idx := -1
	for i, w := range ws {
		if !w.End.After(t) {
			idx = i
		}
	}
	return idx
}

// DetectionTimes extracts the bucket times a run flagged at or above minScore —
// the detector's positive set for NAB scoring.
func DetectionTimes(results []core.BucketResult, minScore float64) []time.Time {
	var out []time.Time
	for _, br := range results {
		for _, r := range br.Records {
			if r.Score >= minScore {
				out = append(out, br.Time)
				break
			}
		}
	}
	return out
}

// LoadNABCSV reads a NAB data file (a `timestamp,value` CSV, RFC3339 or
// "2006-01-02 15:04:05" timestamps) into data points.
func LoadNABCSV(r io.Reader) ([]core.DataPoint, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	pts := make([]core.DataPoint, 0, len(rows))
	for i, row := range rows {
		if len(row) < 2 {
			continue
		}
		if i == 0 && (row[0] == "timestamp" || row[1] == "value") {
			continue // header
		}
		t, err := parseNABTime(row[0])
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", i, err)
		}
		var v float64
		if _, err := fmt.Sscanf(row[1], "%g", &v); err != nil {
			return nil, fmt.Errorf("row %d value %q: %w", i, row[1], err)
		}
		pts = append(pts, core.DataPoint{Time: t, Value: v})
	}
	return pts, nil
}

// ParseNABWindows parses NAB's window JSON for one data file: a list of
// [start, end] timestamp-string pairs, e.g. [["2014-04-10 ...","2014-04-11 ..."]].
func ParseNABWindows(r io.Reader) ([]AnomalyWindow, error) {
	var raw [][]string
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]AnomalyWindow, 0, len(raw))
	for _, pair := range raw {
		if len(pair) != 2 {
			return nil, fmt.Errorf("window %v: expected [start, end]", pair)
		}
		s, err := parseNABTime(pair[0])
		if err != nil {
			return nil, err
		}
		e, err := parseNABTime(pair[1])
		if err != nil {
			return nil, err
		}
		out = append(out, AnomalyWindow{Start: s, End: e})
	}
	return out, nil
}

func parseNABTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999", "2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable timestamp %q", s)
}
