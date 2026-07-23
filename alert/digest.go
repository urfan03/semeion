package alert

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Digest batches lower-urgency alerts and folds them into a single periodic
// summary, instead of paging on each — the counterpart to immediate paging for a
// noisy-but-benign signal. A caller routes sub-page-threshold alerts here and
// flushes on an interval (or at end of a run), delivering one combined message.
type Digest struct {
	mu    sync.Mutex
	items []Alert
}

// NewDigest returns an empty digest.
func NewDigest() *Digest { return &Digest{} }

// Add batches one alert.
func (d *Digest) Add(a Alert) {
	d.mu.Lock()
	d.items = append(d.items, a)
	d.mu.Unlock()
}

// Len is the number of batched alerts.
func (d *Digest) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.items)
}

// Flush produces one summary Alert covering the batch and clears it. ok is false
// when nothing was batched. The summary carries the batch's max score/severity,
// its time span, and a per-detector breakdown, so a single message conveys the
// whole quiet-hours backlog.
func (d *Digest) Flush() (summary Alert, count int, ok bool) {
	d.mu.Lock()
	items := d.items
	d.items = nil
	d.mu.Unlock()

	if len(items) == 0 {
		return Alert{}, 0, false
	}
	byDet := map[string]int{}
	var maxScore float64
	var top Alert
	first, last := items[0].Time, items[0].Time
	for _, a := range items {
		byDet[a.Detector]++
		if a.Score > maxScore {
			maxScore, top = a.Score, a
		}
		if a.Time.Before(first) {
			first = a.Time
		}
		if a.Time.After(last) {
			last = a.Time
		}
	}
	type kv struct {
		det string
		n   int
	}
	rows := make([]kv, 0, len(byDet))
	for det, n := range byDet {
		rows = append(rows, kv{det, n})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].det < rows[j].det
	})
	var b strings.Builder
	fmt.Fprintf(&b, "%d anomalies %s–%s. Top detectors: ",
		len(items), first.UTC().Format("15:04"), last.UTC().Format("15:04"))
	for i, r := range rows {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s×%d", r.det, r.n)
		if i >= 4 {
			fmt.Fprintf(&b, " (+%d more)", len(rows)-i-1)
			break
		}
	}
	return Alert{
		Job:      top.Job,
		Time:     last,
		Detector: "digest",
		Series:   fmt.Sprintf("%d alerts", len(items)),
		Score:    maxScore,
		Kind:     "digest",
		Note:     b.String(),
	}, len(items), true
}
