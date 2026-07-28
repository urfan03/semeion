package benchmark

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseUCRName(t *testing.T) {
	name, trainEnd, start, end, err := ParseUCRName("001_UCR_Anomaly_DISTORTED1sddb40_35000_52000_52620.txt")
	if err != nil {
		t.Fatal(err)
	}
	if name != "001_UCR_Anomaly_DISTORTED1sddb40" {
		t.Fatalf("unexpected name %q", name)
	}
	if trainEnd != 35000 || start != 52000 || end != 52620 {
		t.Fatalf("unexpected fields: %d %d %d", trainEnd, start, end)
	}
	if _, _, _, _, err := ParseUCRName("nope.txt"); err == nil {
		t.Fatal("a name without the trailing indices must be rejected")
	}
	if _, _, _, _, err := ParseUCRName("a_UCR_Anomaly_x_100_200_abc.txt"); err == nil {
		t.Fatal("a non-numeric index must be rejected")
	}
	if _, _, _, _, err := ParseUCRName("a_UCR_Anomaly_x_100_300_200.txt"); err == nil {
		t.Fatal("an inverted anomaly range must be rejected")
	}
}

func TestReadUCRValues(t *testing.T) {
	vals, err := ReadUCRValues(strings.NewReader("1.0\n2.5\n\n3 4\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 4 || vals[1] != 2.5 || vals[3] != 4 {
		t.Fatalf("unexpected values: %v", vals)
	}
	if _, err := ReadUCRValues(strings.NewReader("1.0\nnot-a-number\n")); err == nil {
		t.Fatal("a malformed value must error")
	}
	if _, err := ReadUCRValues(strings.NewReader("\n\n")); err == nil {
		t.Fatal("an empty file must error")
	}
}

func TestLoadUCRCorpus(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < 200; i++ {
		v := "1.0"
		if i >= 150 && i <= 159 {
			v = "9.0"
		}
		b.WriteString(v + "\n")
	}
	path := filepath.Join(dir, "007_UCR_Anomaly_synthetic_100_150_159.txt")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("ignore me"), 0o644); err != nil {
		t.Fatal(err)
	}

	series, err := LoadUCRCorpus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 1 {
		t.Fatalf("expected 1 series, got %d", len(series))
	}
	s := series[0]
	if len(s.Points) != 200 || s.Anomalies != 10 {
		t.Fatalf("unexpected series: %d points, %d anomalies", len(s.Points), s.Anomalies)
	}
	if !s.Labels[150] || !s.Labels[159] || s.Labels[149] || s.Labels[160] {
		t.Fatal("labels must cover exactly the named range")
	}
	if len(s.Windows) != 1 || !s.Windows[0].Start.Equal(s.Points[150].Time) {
		t.Fatalf("window must match the labelled range: %+v", s.Windows)
	}

	_, sum := RunCorpus(series, func(c CorpusSeries) []float64 { return c.Values() })
	if sum.Scored != 1 || sum.MacroF1 != 1 {
		t.Fatalf("the raw value is a perfect detector here: %+v", sum)
	}

	if _, err := LoadUCRCorpus(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing directory must error")
	}
	if _, err := LoadUCRCorpus(t.TempDir()); err == nil {
		t.Fatal("a directory with no UCR files must error")
	}
}

func TestLoadUCRFileClampsEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "001_UCR_Anomaly_short_10_20_999.txt")
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString("1.0\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := LoadUCRFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if u.End != 49 {
		t.Fatalf("an end past the series must clamp to the last index, got %d", u.End)
	}

	bad := filepath.Join(dir, "002_UCR_Anomaly_short_10_400_450.txt")
	if err := os.WriteFile(bad, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUCRFile(bad); err == nil {
		t.Fatal("a start past the series must error")
	}
}
