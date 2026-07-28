package benchmark

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseCombinedWindows(t *testing.T) {
	src := `{"a/x.csv": [["2014-04-01 00:02:00", "2014-04-01 00:08:00"]], "a/y.csv": []}`
	m, err := ParseCombinedWindows(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || len(m["a/x.csv"]) != 1 || len(m["a/y.csv"]) != 0 {
		t.Fatalf("unexpected windows: %+v", m)
	}
	if !m["a/x.csv"][0].Start.Equal(time.Date(2014, 4, 1, 0, 2, 0, 0, time.UTC)) {
		t.Fatalf("window start wrong: %v", m["a/x.csv"][0].Start)
	}
	if _, err := ParseCombinedWindows(strings.NewReader(`{"a.csv": [["only-one"]]}`)); err == nil {
		t.Fatal("a malformed window pair must be rejected")
	}
}

func TestLoadCorpusAndRun(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "data", "grp")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	var csv strings.Builder
	csv.WriteString("timestamp,value\n")
	base := time.Date(2014, 4, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 40; i++ {
		v := "1.0"
		if i >= 20 && i < 24 {
			v = "50.0"
		}
		csv.WriteString(base.Add(time.Duration(i)*time.Minute).Format("2006-01-02 15:04:05") + "," + v + "\n")
	}
	if err := os.WriteFile(filepath.Join(sub, "s.csv"), []byte(csv.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "clean.csv"), []byte("timestamp,value\n2014-04-01 00:00:00,1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	winPath := filepath.Join(dir, "windows.json")
	win := `{"grp/s.csv": [["2014-04-01 00:20:00","2014-04-01 00:23:00"]], "grp/clean.csv": []}`
	if err := os.WriteFile(winPath, []byte(win), 0o644); err != nil {
		t.Fatal(err)
	}

	series, err := LoadCorpus(filepath.Join(dir, "data"), winPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 2 {
		t.Fatalf("expected 2 series, got %d", len(series))
	}
	var target CorpusSeries
	for _, s := range series {
		if s.Key == "grp/s.csv" {
			target = s
		}
	}
	if target.Anomalies != 4 {
		t.Fatalf("expected 4 labelled points, got %d", target.Anomalies)
	}
	if len(target.Values()) != len(target.Points) {
		t.Fatal("Values must match the point count")
	}

	results, sum := RunCorpus(series, func(s CorpusSeries) []float64 { return s.Values() })
	if sum.Series != 2 || sum.Scored != 1 || sum.Skipped != 1 {
		t.Fatalf("summary counts wrong: %+v", sum)
	}
	if sum.MacroF1 != 1 || sum.MicroF1 != 1 {
		t.Fatalf("the value itself is a perfect detector here: %+v", sum)
	}
	for _, r := range results {
		if r.Key == "grp/clean.csv" && (!r.Skipped || r.SkipReason == "") {
			t.Fatalf("anomaly-free series must be skipped: %+v", r)
		}
	}

	_, badSum := RunCorpus(series, func(s CorpusSeries) []float64 { return nil })
	if badSum.Scored != 0 || badSum.Skipped != 2 {
		t.Fatalf("a detector returning the wrong length must be skipped: %+v", badSum)
	}

	if _, err := LoadCorpus(filepath.Join(dir, "data"), filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("a missing windows file must error")
	}
}

func TestLocateCorpusFindsBothLayouts(t *testing.T) {
	root := t.TempDir()
	if _, _, err := LocateCorpus(root); err == nil {
		t.Fatal("a directory without data/ must not look like a NAB checkout")
	}
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LocateCorpus(root); err == nil {
		t.Fatal("data/ alone is not enough — the labels must be found too")
	}

	flat := filepath.Join(root, "combined_windows.json")
	if err := os.WriteFile(flat, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	dataDir, windows, err := LocateCorpus(root)
	if err != nil {
		t.Fatal(err)
	}
	if dataDir != filepath.Join(root, "data") || windows != flat {
		t.Fatalf("root layout resolved wrong: %s %s", dataDir, windows)
	}

	labelled := filepath.Join(root, "labels", "combined_windows.json")
	if err := os.MkdirAll(filepath.Dir(labelled), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(labelled, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, windows, err = LocateCorpus(root); err != nil {
		t.Fatal(err)
	}
	if windows != labelled {
		t.Fatalf("an upstream NAB checkout keeps labels in labels/, got %s", windows)
	}

	if _, err := LoadCorpusRoot(filepath.Join(root, "nope")); err == nil {
		t.Fatal("a missing root must error")
	}
}
