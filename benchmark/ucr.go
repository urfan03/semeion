package benchmark

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/urfan03/semeion/core"
)

type UCRSeries struct {
	Name     string
	Values   []float64
	TrainEnd int
	Start    int
	End      int
}

func ParseUCRName(base string) (name string, trainEnd, start, end int, err error) {
	base = strings.TrimSuffix(base, filepath.Ext(base))
	parts := strings.Split(base, "_")
	if len(parts) < 4 {
		return "", 0, 0, 0, fmt.Errorf("%q: expected <id>_UCR_Anomaly_<name>_<trainEnd>_<start>_<end>", base)
	}
	nums := parts[len(parts)-3:]
	vals := make([]int, 3)
	for i, s := range nums {
		v, convErr := strconv.Atoi(s)
		if convErr != nil {
			return "", 0, 0, 0, fmt.Errorf("%q: trailing field %q is not a number", base, s)
		}
		vals[i] = v
	}
	if vals[1] > vals[2] {
		return "", 0, 0, 0, fmt.Errorf("%q: anomaly start %d is after end %d", base, vals[1], vals[2])
	}
	return strings.Join(parts[:len(parts)-3], "_"), vals[0], vals[1], vals[2], nil
}

func ReadUCRValues(r io.Reader) ([]float64, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	var out []float64
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		for _, tok := range strings.Fields(strings.ReplaceAll(line, ",", " ")) {
			v, err := strconv.ParseFloat(tok, 64)
			if err != nil {
				return nil, fmt.Errorf("value %q: %w", tok, err)
			}
			out = append(out, v)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no numeric values found")
	}
	return out, nil
}

func LoadUCRFile(path string) (UCRSeries, error) {
	name, trainEnd, start, end, err := ParseUCRName(filepath.Base(path))
	if err != nil {
		return UCRSeries{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return UCRSeries{}, err
	}
	defer f.Close()
	values, err := ReadUCRValues(f)
	if err != nil {
		return UCRSeries{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if start >= len(values) {
		return UCRSeries{}, fmt.Errorf("%s: anomaly start %d is past the %d values", filepath.Base(path), start, len(values))
	}
	if end >= len(values) {
		end = len(values) - 1
	}
	return UCRSeries{Name: name, Values: values, TrainEnd: trainEnd, Start: start, End: end}, nil
}

func (u UCRSeries) Corpus(key string) CorpusSeries {
	base := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	pts := make([]core.DataPoint, len(u.Values))
	labels := make([]bool, len(u.Values))
	for i, v := range u.Values {
		pts[i] = core.DataPoint{Time: base.Add(time.Duration(i) * time.Minute), Value: v}
		if i >= u.Start && i <= u.End {
			labels[i] = true
		}
	}
	win := []AnomalyWindow{{Start: pts[u.Start].Time, End: pts[u.End].Time}}
	return CorpusSeries{
		Key:       key,
		Points:    pts,
		Windows:   win,
		Labels:    labels,
		Anomalies: u.End - u.Start + 1,
	}
}

func LoadUCRCorpus(dir string) ([]CorpusSeries, error) {
	var out []CorpusSeries
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".txt" && ext != ".csv" && ext != ".out" {
			return nil
		}
		if !strings.Contains(strings.ToUpper(filepath.Base(path)), "UCR_ANOMALY") {
			return nil
		}
		u, err := LoadUCRFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			rel = filepath.Base(path)
		}
		out = append(out, u.Corpus(filepath.ToSlash(rel)))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no UCR_Anomaly files found under %s", dir)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}
