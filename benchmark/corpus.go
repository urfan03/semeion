package benchmark

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/urfan03/semeion/core"
)

type CorpusSeries struct {
	Key       string
	Points    []core.DataPoint
	Windows   []AnomalyWindow
	Labels    []bool
	Anomalies int
}

func ParseCombinedWindows(r io.Reader) (map[string][]AnomalyWindow, error) {
	var raw map[string][][]string
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, err
	}
	out := make(map[string][]AnomalyWindow, len(raw))
	for key, pairs := range raw {
		ws := make([]AnomalyWindow, 0, len(pairs))
		for _, pair := range pairs {
			if len(pair) != 2 {
				return nil, fmt.Errorf("%s: window %v: expected [start, end]", key, pair)
			}
			s, err := parseNABTime(pair[0])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			e, err := parseNABTime(pair[1])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			ws = append(ws, AnomalyWindow{Start: s, End: e})
		}
		out[key] = ws
	}
	return out, nil
}

func LabelPoints(points []core.DataPoint, windows []AnomalyWindow) []bool {
	labels := make([]bool, len(points))
	for i, p := range points {
		for _, w := range windows {
			if !p.Time.Before(w.Start) && !p.Time.After(w.End) {
				labels[i] = true
				break
			}
		}
	}
	return labels
}

func LocateCorpus(root string) (string, string, error) {
	dataDir := filepath.Join(root, "data")
	if _, err := os.Stat(dataDir); err != nil {
		return "", "", fmt.Errorf("%s does not look like a NAB checkout: %w", root, err)
	}
	for _, rel := range []string{
		filepath.Join("labels", "combined_windows.json"),
		"combined_windows.json",
	} {
		p := filepath.Join(root, rel)
		if _, err := os.Stat(p); err == nil {
			return dataDir, p, nil
		}
	}
	return "", "", fmt.Errorf("no combined_windows.json under %s (looked in labels/ and the root)", root)
}

func LoadCorpusRoot(root string) ([]CorpusSeries, error) {
	dataDir, windowsPath, err := LocateCorpus(root)
	if err != nil {
		return nil, err
	}
	return LoadCorpus(dataDir, windowsPath)
}

func LoadCorpus(dataDir, windowsPath string) ([]CorpusSeries, error) {
	wf, err := os.Open(windowsPath)
	if err != nil {
		return nil, err
	}
	defer wf.Close()
	windows, err := ParseCombinedWindows(wf)
	if err != nil {
		return nil, err
	}

	var out []CorpusSeries
	err = filepath.WalkDir(dataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".csv") {
			return nil
		}
		rel, err := filepath.Rel(dataDir, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		ws, known := windows[key]
		if !known {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		pts, err := LoadNABCSV(f)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		labels := LabelPoints(pts, ws)
		n := 0
		for _, l := range labels {
			if l {
				n++
			}
		}
		out = append(out, CorpusSeries{Key: key, Points: pts, Windows: ws, Labels: labels, Anomalies: n})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	if len(out) == 0 {
		return nil, fmt.Errorf("no labelled CSV series found under %s", dataDir)
	}
	return out, nil
}

func (s CorpusSeries) Values() []float64 {
	v := make([]float64, len(s.Points))
	for i, p := range s.Points {
		v[i] = p.Value
	}
	return v
}

type ScoreFunc func(CorpusSeries) []float64

type ThresholdFunc func(CorpusSeries, []float64) float64

func QuantileThreshold(p float64) ThresholdFunc {
	return func(_ CorpusSeries, scores []float64) float64 {
		clean := make([]float64, 0, len(scores))
		for _, v := range scores {
			if !math.IsNaN(v) && !math.IsInf(v, 0) {
				clean = append(clean, v)
			}
		}
		if len(clean) == 0 {
			return math.Inf(1)
		}
		sort.Float64s(clean)
		pos := p * float64(len(clean)-1)
		i := int(pos)
		if i >= len(clean)-1 {
			return clean[len(clean)-1]
		}
		return clean[i] + (pos-float64(i))*(clean[i+1]-clean[i])
	}
}

type CorpusOptions struct {
	VUSBuffer int
	PAK       float64
	Range     RangeOptions
	Threshold ThresholdFunc
}

func autoBuffer(labels []bool, want int) int {
	if want >= 0 {
		return want
	}
	segs := Segments(labels)
	if len(segs) == 0 {
		return 0
	}
	lens := make([]int, 0, len(segs))
	for _, s := range segs {
		lens = append(lens, s[1]-s[0]+1)
	}
	sort.Ints(lens)
	b := lens[len(lens)/2]
	if b > 100 {
		b = 100
	}
	return b
}

type CorpusResult struct {
	Key        string  `json:"key"`
	Points     int     `json:"points"`
	Anomalies  int     `json:"anomalies"`
	F1         float64 `json:"pa_f1"`
	Threshold  float64 `json:"threshold"`
	Precision  float64 `json:"pa_precision"`
	Recall     float64 `json:"pa_recall"`
	AUCPR      float64 `json:"aucpr"`
	TP         int     `json:"tp"`
	FP         int     `json:"fp"`
	FN         int     `json:"fn"`
	PAKF1      float64 `json:"pak_f1,omitempty"`
	RangeF1    float64 `json:"range_f1,omitempty"`
	VUSROC     float64 `json:"vus_roc,omitempty"`
	VUSPR      float64 `json:"vus_pr,omitempty"`
	FixedF1    float64 `json:"fixed_pa_f1,omitempty"`
	FixedRawF1 float64 `json:"fixed_raw_f1,omitempty"`
	FixedRawP  float64 `json:"fixed_raw_precision,omitempty"`
	FixedRawR  float64 `json:"fixed_raw_recall,omitempty"`
	FixedAt    float64 `json:"fixed_threshold,omitempty"`
	Events     int     `json:"events,omitempty"`
	EventsHit  int     `json:"events_hit,omitempty"`
	Alarms     int     `json:"alarms,omitempty"`
	Skipped    bool    `json:"skipped,omitempty"`
	SkipReason string  `json:"skip_reason,omitempty"`
}

type CorpusSummary struct {
	Series      int     `json:"series"`
	Scored      int     `json:"scored"`
	Skipped     int     `json:"skipped"`
	MacroF1     float64 `json:"macro_pa_f1"`
	MacroAUCPR  float64 `json:"macro_aucpr"`
	MicroF1     float64 `json:"micro_pa_f1"`
	Precision   float64 `json:"micro_pa_precision"`
	Recall      float64 `json:"micro_pa_recall"`
	TP          int     `json:"tp"`
	FP          int     `json:"fp"`
	FN          int     `json:"fn"`
	MacroPAKF1  float64 `json:"macro_pak_f1,omitempty"`
	MacroRange  float64 `json:"macro_range_f1,omitempty"`
	MacroVUSROC float64 `json:"macro_vus_roc,omitempty"`
	MacroVUSPR  float64 `json:"macro_vus_pr,omitempty"`
	MacroFixed  float64 `json:"macro_fixed_pa_f1,omitempty"`
	MacroRawF1  float64 `json:"macro_fixed_raw_f1,omitempty"`
	MacroRawP   float64 `json:"macro_fixed_raw_precision,omitempty"`
	MacroRawR   float64 `json:"macro_fixed_raw_recall,omitempty"`
	FixedTP     int     `json:"fixed_tp,omitempty"`
	FixedFP     int     `json:"fixed_fp,omitempty"`
	FixedFN     int     `json:"fixed_fn,omitempty"`
	RawTP       int     `json:"fixed_raw_tp,omitempty"`
	RawFP       int     `json:"fixed_raw_fp,omitempty"`
	RawFN       int     `json:"fixed_raw_fn,omitempty"`

	Events      int `json:"events,omitempty"`
	EventsHit   int `json:"events_hit,omitempty"`
	Alarms      int `json:"alarms,omitempty"`
	FalseAlarms int `json:"false_alarms,omitempty"`
}

func (s CorpusSummary) EventRecall() float64 {
	if s.Events == 0 {
		return 0
	}
	return float64(s.EventsHit) / float64(s.Events)
}

func (s CorpusSummary) AlarmPrecision() float64 {
	if s.Alarms == 0 {
		return 0
	}
	return float64(s.Alarms-s.FalseAlarms) / float64(s.Alarms)
}

func RunCorpus(series []CorpusSeries, fn ScoreFunc) ([]CorpusResult, CorpusSummary) {
	return RunCorpusWith(series, fn, CorpusOptions{})
}

func RunCorpusWith(series []CorpusSeries, fn ScoreFunc, opt CorpusOptions) ([]CorpusResult, CorpusSummary) {
	results := make([]CorpusResult, 0, len(series))
	sum := CorpusSummary{Series: len(series)}
	var f1Total, aucTotal, pakTotal, rangeTotal, vusROCTotal, vusPRTotal, fixedTotal float64
	var rawF1Total, rawPTotal, rawRTotal float64
	for _, s := range series {
		res := CorpusResult{Key: s.Key, Points: len(s.Points), Anomalies: s.Anomalies}
		if s.Anomalies == 0 {
			res.Skipped, res.SkipReason = true, "no labelled anomalies"
			sum.Skipped++
			results = append(results, res)
			continue
		}
		scores := fn(s)
		if len(scores) != len(s.Points) {
			res.Skipped, res.SkipReason = true, "detector returned wrong length"
			sum.Skipped++
			results = append(results, res)
			continue
		}
		best, thr := BestPointAdjustedF1(scores, s.Labels)
		res.F1, res.Threshold = best.F1, thr
		res.Precision, res.Recall = best.Precision, best.Recall
		res.TP, res.FP, res.FN = best.TP, best.FP, best.FN
		res.AUCPR = AUCPR(scores, s.Labels)

		if opt.PAK > 0 {
			pak, _ := BestPointAdjustedKF1(scores, s.Labels, opt.PAK)
			res.PAKF1 = pak.F1
			pakTotal += pak.F1
		}
		if opt.Range != (RangeOptions{}) {
			_, _, rf1, _ := BestRangeF1(scores, s.Labels, opt.Range)
			res.RangeF1 = rf1
			rangeTotal += rf1
		}
		if opt.VUSBuffer != 0 {
			roc, pr := VUS(scores, s.Labels, autoBuffer(s.Labels, opt.VUSBuffer))
			res.VUSROC, res.VUSPR = roc, pr
			vusROCTotal += roc
			vusPRTotal += pr
		}
		if opt.Threshold != nil {
			at := opt.Threshold(s, scores)
			pred := make([]bool, len(s.Labels))
			for i := range pred {
				pred[i] = scores[i] >= at
			}
			fixed := PointAdjustedScore(pred, s.Labels)
			res.FixedF1, res.FixedAt = fixed.F1, at
			fixedTotal += fixed.F1
			sum.FixedTP += fixed.TP
			sum.FixedFP += fixed.FP
			sum.FixedFN += fixed.FN

			raw := Confusion(pred, s.Labels)
			res.FixedRawF1, res.FixedRawP, res.FixedRawR = raw.F1, raw.Precision, raw.Recall
			rawF1Total += raw.F1
			rawPTotal += raw.Precision
			rawRTotal += raw.Recall
			sum.RawTP += raw.TP
			sum.RawFP += raw.FP
			sum.RawFN += raw.FN

			segs := Segments(s.Labels)
			hit := 0
			for _, g := range segs {
				for k := g[0]; k <= g[1] && k < len(pred); k++ {
					if pred[k] {
						hit++
						break
					}
				}
			}
			alarms := 0
			for i, p := range pred {
				if p {
					alarms++
					if !s.Labels[i] {
						sum.FalseAlarms++
					}
				}
			}
			res.Events, res.EventsHit, res.Alarms = len(segs), hit, alarms
			sum.Events += len(segs)
			sum.EventsHit += hit
			sum.Alarms += alarms
		}

		sum.Scored++
		sum.TP += best.TP
		sum.FP += best.FP
		sum.FN += best.FN
		f1Total += best.F1
		aucTotal += res.AUCPR
		results = append(results, res)
	}
	if sum.Scored > 0 {
		n := float64(sum.Scored)
		sum.MacroF1 = f1Total / n
		sum.MacroAUCPR = aucTotal / n
		sum.MacroPAKF1 = pakTotal / n
		sum.MacroRange = rangeTotal / n
		sum.MacroVUSROC = vusROCTotal / n
		sum.MacroVUSPR = vusPRTotal / n
		sum.MacroFixed = fixedTotal / n
		sum.MacroRawF1 = rawF1Total / n
		sum.MacroRawP = rawPTotal / n
		sum.MacroRawR = rawRTotal / n
	}
	if sum.TP+sum.FP > 0 {
		sum.Precision = float64(sum.TP) / float64(sum.TP+sum.FP)
	}
	if sum.TP+sum.FN > 0 {
		sum.Recall = float64(sum.TP) / float64(sum.TP+sum.FN)
	}
	if sum.Precision+sum.Recall > 0 {
		sum.MicroF1 = 2 * sum.Precision * sum.Recall / (sum.Precision + sum.Recall)
	}
	return results, sum
}
