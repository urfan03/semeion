package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/urfan03/semeion/benchmark"
	"github.com/urfan03/semeion/evt"
	"github.com/urfan03/semeion/fuse"
	"github.com/urfan03/semeion/guard"
	"github.com/urfan03/semeion/hst"
	"github.com/urfan03/semeion/mp"
	"github.com/urfan03/semeion/prep"
	"github.com/urfan03/semeion/sub"
)

func corpusEVTOptions(n int) evt.StreamOptions {
	c := n / 5
	if c < 200 {
		c = 200
	}
	if c > 800 {
		c = 800
	}
	return evt.StreamOptions{Calibration: c, Drift: true}
}

func corpusSubOptions() sub.Options { return sub.Options{Window: 16, K: 5} }

func corpusEVTProbabilities(s benchmark.CorpusSeries) []float64 {
	v := s.Values()
	return evt.TwoSidedProbabilities(v, corpusEVTOptions(len(v)))
}

func corpusResidual(s benchmark.CorpusSeries) []float64 {
	r, _ := prep.Deseasonalize(s.Values(), prep.Options{})
	return r
}

func corpusStreams(values []float64) [][]float64 {
	warm := corpusWarmup(len(values))
	return [][]float64{
		evt.TwoSidedProbabilities(values, corpusEVTOptions(len(values))),
		fuse.PValues(mp.Scores(values, mp.Options{}), warm),
		fuse.PValues(hst.Series(values, hst.SeriesOptions{}), warm),
	}
}

var corpusBase = map[string]benchmark.ScoreFunc{
	"mp":   func(s benchmark.CorpusSeries) []float64 { return mp.Scores(s.Values(), mp.Options{}) },
	"damp": func(s benchmark.CorpusSeries) []float64 { return mp.DAMP(s.Values(), mp.DAMPOptions{}) },
	"hst":  func(s benchmark.CorpusSeries) []float64 { return hst.Series(s.Values(), hst.SeriesOptions{}) },
	"evt": func(s benchmark.CorpusSeries) []float64 {
		return fuse.NegLog10(corpusEVTProbabilities(s))
	},
	"sub-knn": func(s benchmark.CorpusSeries) []float64 { return sub.KNN(s.Values(), corpusSubOptions()) },
	"sub-lof": func(s benchmark.CorpusSeries) []float64 { return sub.LOF(s.Values(), corpusSubOptions()) },
	"sub-pca": func(s benchmark.CorpusSeries) []float64 {
		return sub.PCA(s.Values(), sub.PCAOptions{Options: corpusSubOptions(), Variance: 0.9})
	},
	"sub-iforest": func(s benchmark.CorpusSeries) []float64 {
		return sub.IForest(s.Values(), sub.ForestOptions{Options: corpusSubOptions(), Trees: 100, SampleSize: 256, Seed: 11})
	},
}

func corpusWarmup(n int) int {
	w := n / 10
	if w < 50 {
		w = 50
	}
	return w
}

func corpusPValues(s benchmark.CorpusSeries, names []string) [][]float64 {
	streams := make([][]float64, 0, len(names))
	for _, n := range names {
		if n == "evt" {
			streams = append(streams, corpusEVTProbabilities(s))
			continue
		}
		fn, ok := corpusBase[n]
		if !ok {
			continue
		}
		streams = append(streams, fuse.PValues(fn(s), corpusWarmup(len(s.Points))))
	}
	return streams
}

func corpusDetectorNames() []string {
	names := make([]string, 0, len(corpusBase)+3)
	for n := range corpusBase {
		names = append(names, n)
	}
	sort.Strings(names)
	return append(names, "fisher", "fisher-all", "weighted", "agree", "multiscale")
}

func corpusDetector(name string) (benchmark.ScoreFunc, error) {
	if fn, ok := corpusBase[name]; ok {
		return fn, nil
	}
	all := []string{"damp", "evt", "hst", "mp", "sub-iforest", "sub-knn", "sub-lof", "sub-pca"}
	switch name {
	case "fisher":
		return func(s benchmark.CorpusSeries) []float64 {
			return fuse.NegLog10(fuse.FisherStreams(corpusPValues(s, []string{"evt", "mp", "hst"})))
		}, nil
	case "fisher-all":
		return func(s benchmark.CorpusSeries) []float64 {
			return fuse.NegLog10(fuse.FisherStreams(corpusPValues(s, all)))
		}, nil
	case "weighted":
		return func(s benchmark.CorpusSeries) []float64 {
			combined, _ := fuse.WeightedCombine(corpusPValues(s, all), fuse.WeightedOptions{Warmup: corpusWarmup(len(s.Points))})
			return fuse.NegLog10(combined)
		}, nil
	case "agree":
		return func(s benchmark.CorpusSeries) []float64 {
			return fuse.NegLog10(fuse.AgreeStreams(corpusStreams(corpusResidual(s)), 2))
		}, nil
	case "multiscale":
		return func(s benchmark.CorpusSeries) []float64 {
			r := corpusResidual(s)
			warm := corpusWarmup(len(r))
			streams := [][]float64{evt.TwoSidedProbabilities(r, corpusEVTOptions(len(r)))}
			streams = append(streams, fuse.MultiScale(fuse.Scales(8, 3), warm, func(w int) []float64 {
				return mp.DAMP(r, mp.DAMPOptions{Window: w})
			})...)
			streams = append(streams, fuse.PValues(hst.Series(r, hst.SeriesOptions{}), warm))
			return fuse.NegLog10(fuse.AgreeStreams(streams, 2))
		}, nil
	}
	return nil, fmt.Errorf("unknown detector %q (one of: %s)", name, strings.Join(corpusDetectorNames(), ", "))
}

func corpusThreshold(_ benchmark.CorpusSeries, scores []float64) float64 {
	z, _, ok := evt.POT(scores, evt.Options{Q: 1e-3, Level: 0.98})
	if !ok {
		return math.Inf(1)
	}
	return z
}

func runNABCorpus(args []string) error {
	fs := flag.NewFlagSet("nab-corpus", flag.ContinueOnError)
	dir := fs.String("dir", "", "NAB checkout (data/ + combined_windows.json) (required unless --ucr)")
	ucr := fs.String("ucr", "", "UCR Anomaly Archive directory (*_UCR_Anomaly_*.txt)")
	detector := fs.String("detector", "fisher", "detector: "+strings.Join(corpusDetectorNames(), ", "))
	perSeries := fs.Bool("per-series", false, "print one line per series")
	asJSON := fs.Bool("json", false, "emit the summary as JSON")
	full := fs.Bool("full-metrics", true, "also compute PA%K, range-F1, VUS and a fixed EVT operating point")
	policy := fs.String("policy", "", "alarm policy for the event-level report: sensitive, balanced, precise, paranoid")
	q := fs.Float64("q", 1e-3, "EVT tail probability for the alarm threshold")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *policy != "" {
		if _, ok := guard.Presets()[*policy]; !ok {
			return fmt.Errorf("unknown policy %q (sensitive, balanced, precise, paranoid)", *policy)
		}
	}
	if *dir == "" && *ucr == "" {
		return fmt.Errorf("--dir or --ucr is required")
	}
	fn, err := corpusDetector(*detector)
	if err != nil {
		return err
	}

	var series []benchmark.CorpusSeries
	if *ucr != "" {
		series, err = benchmark.LoadUCRCorpus(*ucr)
	} else {
		series, err = benchmark.LoadCorpusRoot(*dir)
	}
	if err != nil {
		return err
	}

	opt := benchmark.CorpusOptions{}
	if *full {
		opt = benchmark.CorpusOptions{
			VUSBuffer: -1,
			PAK:       0.2,
			Range:     benchmark.RangeOptions{Alpha: 0.5, Bias: benchmark.BiasFlat},
			Threshold: corpusThreshold,
		}
	}
	results, sum := benchmark.RunCorpusWith(series, fn, opt)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Detector string                   `json:"detector"`
			Summary  benchmark.CorpusSummary  `json:"summary"`
			Series   []benchmark.CorpusResult `json:"series,omitempty"`
		}{*detector, sum, seriesOrNil(results, *perSeries)})
	}

	if *perSeries {
		sort.Slice(results, func(i, j int) bool { return results[i].F1 < results[j].F1 })
		for _, r := range results {
			if r.Skipped {
				fmt.Printf("  %-52s skipped (%s)\n", r.Key, r.SkipReason)
				continue
			}
			fmt.Printf("  %-52s PA-F1=%.4f VUS-PR=%.4f AUC-PR=%.4f TP=%d FP=%d FN=%d\n",
				r.Key, r.F1, r.VUSPR, r.AUCPR, r.TP, r.FP, r.FN)
		}
	}
	fmt.Printf("%s: %d series (%d scored, %d without labels)\n", *detector, sum.Series, sum.Scored, sum.Skipped)
	fmt.Printf("  point-adjusted F1   macro=%.4f  micro=%.4f (P=%.3f R=%.3f)\n", sum.MacroF1, sum.MicroF1, sum.Precision, sum.Recall)
	if *full {
		fmt.Printf("  PA%%20 F1            %.4f\n", sum.MacroPAKF1)
		fmt.Printf("  range F1            %.4f\n", sum.MacroRange)
		fmt.Printf("  VUS-PR / VUS-ROC    %.4f / %.4f\n", sum.MacroVUSPR, sum.MacroVUSROC)
		fmt.Printf("  fixed-threshold F1  %.4f point-adjusted (EVT operating point)\n", sum.MacroFixed)
		fmt.Printf("  fixed-threshold F1  %.4f raw point-wise (P=%.3f R=%.3f)\n", sum.MacroRawF1, sum.MacroRawP, sum.MacroRawR)
		fmt.Printf("  at that threshold   %d alarms, %d landed outside a window\n", sum.Alarms, sum.FalseAlarms)
		fmt.Printf("  event recall        %.4f (%d of %d anomaly windows caught)\n", sum.EventRecall(), sum.EventsHit, sum.Events)
		fmt.Printf("  alarm precision     %.4f\n", sum.AlarmPrecision())
	}
	fmt.Printf("  AUC-PR              %.4f\n", sum.MacroAUCPR)

	if *policy != "" {
		opt := guard.Presets()[*policy]
		op := benchmark.EventScore(series, fn, func(s benchmark.CorpusSeries, scores []float64) []bool {
			o := opt
			o.Threshold = corpusThresholdAt(*q)(s, scores)
			o.Warmup = corpusWarmupWindow(len(scores))
			return guard.Apply(scores, o)
		}, *policy)
		fmt.Printf("\nalarm policy %q at q=%g:\n", *policy, *q)
		fmt.Printf("  event recall        %.4f (%d of %d anomaly windows)\n", op.EventRecall, op.EventsHit, op.Events)
		fmt.Printf("  alarm precision     %.4f (%d alarms, %d outside a window)\n", op.AlarmPrecision, op.Alarms, op.FalseAlarms)
		fmt.Printf("  event F1            %.4f\n", op.F1)
		fmt.Printf("  alarm volume        %.2f per series\n", op.AlarmsPerSerie)
	}
	return nil
}

func corpusWarmupWindow(n int) int {
	c := n / 5
	if c < 200 {
		c = 200
	}
	if c > 800 {
		c = 800
	}
	return c
}

func corpusThresholdAt(q float64) benchmark.ThresholdFunc {
	return func(s benchmark.CorpusSeries, scores []float64) float64 {
		z, _, ok := evt.POT(scores, evt.Options{Q: q, Level: 0.98})
		if !ok {
			return benchmark.QuantileThreshold(0.999)(s, scores)
		}
		return z
	}
}

func seriesOrNil(results []benchmark.CorpusResult, include bool) []benchmark.CorpusResult {
	if !include {
		return nil
	}
	return results
}
