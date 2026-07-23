package alert

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Digest struct {
	mu    sync.Mutex
	items []Alert
}

func NewDigest() *Digest { return &Digest{} }

func (d *Digest) Add(a Alert) {
	d.mu.Lock()
	d.items = append(d.items, a)
	d.mu.Unlock()
}

func (d *Digest) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.items)
}

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
