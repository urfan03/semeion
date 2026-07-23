package ingest

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/urfan03/semeion/core"
)

func ReadCSV(path, timeCol, valueCol string) ([]core.DataPoint, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseCSV(f, timeCol, valueCol)
}

func parseCSV(rd io.Reader, timeCol, valueCol string) ([]core.DataPoint, error) {
	r := csv.NewReader(rd)
	r.TrimLeadingSpace = true

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
	vi, ok := idx[valueCol]
	if !ok {
		return nil, fmt.Errorf("value column %q not found", valueCol)
	}

	var out []core.DataPoint
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
		ts, err := parseTime(rec[ti])
		if err != nil {
			return nil, fmt.Errorf("row %d: time %q: %w", row, rec[ti], err)
		}
		val, err := strconv.ParseFloat(strings.TrimSpace(rec[vi]), 64)
		if err != nil {
			return nil, fmt.Errorf("row %d: value %q: %w", row, rec[vi], err)
		}

		var dims map[string]string
		var vals map[string]float64
		for name, i := range idx {
			if i == ti || i >= len(rec) {
				continue
			}
			s := strings.TrimSpace(rec[i])
			if f, ferr := strconv.ParseFloat(s, 64); ferr == nil {
				if vals == nil {
					vals = make(map[string]float64)
				}
				vals[name] = f
			} else if i != vi {
				if dims == nil {
					dims = make(map[string]string)
				}
				dims[name] = s
			}
		}
		out = append(out, core.DataPoint{Time: ts, Value: val, Fields: dims, Values: vals})
	}
	return out, nil
}

func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n > 1e12 {
			return time.UnixMilli(n).UTC(), nil
		}
		return time.Unix(n, 0).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("unrecognised time format (want RFC3339 or Unix epoch)")
}
