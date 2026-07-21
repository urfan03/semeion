package ingest

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/urfan03/semeion/core"
)

// ReadLogCSV loads log lines from a CSV with a header row: a timestamp column
// and a message column (by name). Any other column becomes a dimension field.
func ReadLogCSV(path, timeCol, msgCol string) ([]core.LogLine, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1 // messages may contain commas within quotes; be lenient
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[strings.TrimSpace(h)] = i
	}
	ti, ok := idx[timeCol]
	if !ok {
		return nil, fmt.Errorf("time column %q not found", timeCol)
	}
	mi, ok := idx[msgCol]
	if !ok {
		return nil, fmt.Errorf("message column %q not found", msgCol)
	}

	var out []core.LogLine
	row := 1
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		row++
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", row, err)
		}
		if ti >= len(rec) || mi >= len(rec) {
			continue
		}
		ts, err := parseTime(rec[ti])
		if err != nil {
			return nil, fmt.Errorf("row %d: time %q: %w", row, rec[ti], err)
		}
		var dims map[string]string
		for name, i := range idx {
			if i == ti || i == mi || i >= len(rec) {
				continue
			}
			if dims == nil {
				dims = make(map[string]string)
			}
			dims[name] = strings.TrimSpace(rec[i])
		}
		out = append(out, core.LogLine{Time: ts, Message: rec[mi], Fields: dims})
	}
	return out, nil
}

// SyntheticLogs builds a deterministic log stream for the demo: two steady
// templates, a brand-new template injected near the end, and a spike of the
// steady template — so new / spike detection both trigger.
func SyntheticLogs(start time.Time, span time.Duration, buckets int) []core.LogLine {
	var out []core.LogLine
	add := func(i, n int, msg string) {
		t := start.Add(time.Duration(i) * span)
		for k := 0; k < n; k++ {
			out = append(out, core.LogLine{Time: t.Add(time.Duration(k) * time.Second), Message: msg})
		}
	}
	for i := 0; i < buckets; i++ {
		add(i, 3, "GET /api/users status 200 in 12 ms")
		add(i, 2, "cache hit for key session-42")
	}
	newAt := buckets * 8 / 10
	spikeAt := buckets*8/10 + 1
	add(newAt, 6, "panic runtime error nil pointer at 0x1a2b3c4d")
	add(spikeAt, 60, "GET /api/users status 200 in 15 ms")
	return out
}
