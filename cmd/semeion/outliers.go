package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/urfan03/semeion/outlier"
)

func runOutliers(args []string) error {
	fs := flag.NewFlagSet("outliers", flag.ContinueOnError)
	csvPath := fs.String("csv", "", "CSV table: one row per entity (required)")
	k := fs.Int("k", 0, "neighbours to compare against (0 = auto)")
	top := fs.Int("top", 10, "how many rows to print (0 = all)")
	raw := fs.Bool("raw", false, "skip per-feature standardization")
	jsonOut := fs.Bool("json", false, "emit JSON instead of a table")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *csvPath == "" {
		return fmt.Errorf("--csv is required")
	}

	features, rows, labels, err := readTable(*csvPath)
	if err != nil {
		return err
	}
	res, err := outlier.Detect(features, rows, outlier.Options{K: *k, Raw: *raw})
	if err != nil {
		return err
	}
	ranked := outlier.Top(res, *top)

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		for _, r := range ranked {
			if err := enc.Encode(map[string]any{
				"index": r.Index, "score": r.Score, "labels": labels[r.Index],
				"methods": r.Methods, "influence": r.Influence,
			}); err != nil {
				return err
			}
		}
		return nil
	}

	fmt.Printf("%d rows × %d features (%s)\n\n", len(rows), len(features), strings.Join(features, ", "))
	for _, r := range ranked {
		fmt.Printf("  %-24s score=%.3f  %s\n", label(labels[r.Index], r.Index), r.Score, topInfluences(r.Influence, 2))
	}
	fmt.Printf("\n(score > 0.5 ≈ beyond 3 robust deviations of the population)\n")
	return nil
}

func label(l map[string]string, idx int) string {
	if len(l) == 0 {
		return fmt.Sprintf("row %d", idx)
	}
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, l[k])
	}
	return strings.Join(parts, "/")
}

func topInfluences(inf map[string]float64, n int) string {
	if len(inf) == 0 {
		return ""
	}
	type kv struct {
		k string
		v float64
	}
	all := make([]kv, 0, len(inf))
	for k, v := range inf {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v })
	if n < len(all) {
		all = all[:n]
	}
	parts := make([]string, 0, len(all))
	for _, e := range all {
		parts = append(parts, fmt.Sprintf("%s %.0f%%", e.k, e.v*100))
	}
	return "← " + strings.Join(parts, ", ")
}

func readTable(path string) ([]string, [][]float64, []map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, nil, err
	}
	defer f.Close()

	rec, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, nil, nil, err
	}
	if len(rec) < 2 {
		return nil, nil, nil, fmt.Errorf("%s: need a header row and at least one data row", path)
	}
	header, data := rec[0], rec[1:]

	numeric := make([]bool, len(header))
	for c := range header {
		numeric[c] = true
		for _, row := range data {
			if c >= len(row) {
				numeric[c] = false
				break
			}
			if _, err := strconv.ParseFloat(strings.TrimSpace(row[c]), 64); err != nil {
				numeric[c] = false
				break
			}
		}
	}

	var features []string
	var cols []int
	for c, name := range header {
		if numeric[c] {
			features = append(features, strings.TrimSpace(name))
			cols = append(cols, c)
		}
	}
	if len(features) == 0 {
		return nil, nil, nil, fmt.Errorf("%s: no fully numeric columns found", path)
	}

	matrix := make([][]float64, len(data))
	labels := make([]map[string]string, len(data))
	for i, row := range data {
		vals := make([]float64, len(cols))
		for j, c := range cols {
			vals[j], _ = strconv.ParseFloat(strings.TrimSpace(row[c]), 64)
		}
		matrix[i] = vals
		for c, name := range header {
			if !numeric[c] && c < len(row) {
				if labels[i] == nil {
					labels[i] = map[string]string{}
				}
				labels[i][strings.TrimSpace(name)] = strings.TrimSpace(row[c])
			}
		}
	}
	return features, matrix, labels, nil
}
