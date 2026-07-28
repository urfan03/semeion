package benchmark

import (
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/urfan03/semeion/conformal"
	"github.com/urfan03/semeion/evt"
	"github.com/urfan03/semeion/fuse"
	"github.com/urfan03/semeion/guard"
	"github.com/urfan03/semeion/hst"
	"github.com/urfan03/semeion/mp"
	"github.com/urfan03/semeion/prep"
)

func calibrationFor(n int) int {
	c := n / 5
	if c < 200 {
		c = 200
	}
	if c > 800 {
		c = 800
	}
	return c
}

func conformalCalibration(n int) int {
	c := n / 3
	if c < 400 {
		c = 400
	}
	if c > 4000 {
		c = 4000
	}
	return c
}

func warmupFor(n int) int {
	w := n / 10
	if w < 50 {
		w = 50
	}
	return w
}

func hstOn(values []float64) []float64 { return hst.Series(values, hst.SeriesOptions{}) }

func dampOn(values []float64, window int) []float64 {
	return mp.DAMP(values, mp.DAMPOptions{Window: window})
}

func evtPOn(values []float64) []float64 {
	return evt.TwoSidedProbabilities(values, evt.StreamOptions{Calibration: calibrationFor(len(values)), Drift: true})
}

func stackScores(s CorpusSeries) map[string][]float64 {
	v := s.Values()
	warm := warmupFor(len(v))
	resid, _ := prep.Deseasonalize(v, prep.Options{})

	rawStreams := [][]float64{
		evtPOn(v),
		fuse.PValues(mp.Scores(v, mp.Options{}), warm),
		fuse.PValues(hstOn(v), warm),
	}
	residStreams := [][]float64{
		evtPOn(resid),
		fuse.PValues(mp.Scores(resid, mp.Options{}), warm),
		fuse.PValues(hstOn(resid), warm),
	}
	scaleStreams := [][]float64{evtPOn(resid)}
	scaleStreams = append(scaleStreams, fuse.MultiScale(fuse.Scales(8, 3), warm, func(w int) []float64 {
		return dampOn(resid, w)
	})...)
	scaleStreams = append(scaleStreams, fuse.PValues(hstOn(resid), warm))

	out := map[string][]float64{
		"1 fisher(raw)":      fuse.NegLog10(fuse.FisherStreams(rawStreams)),
		"2 +deseasonalized":  fuse.NegLog10(fuse.FisherStreams(residStreams)),
		"3 +agreement k=2":   fuse.NegLog10(fuse.AgreeStreams(residStreams, 2)),
		"3b agreement k=3":   fuse.NegLog10(fuse.AgreeStreams(residStreams, 3)),
		"4 +multi-scale":     fuse.NegLog10(fuse.AgreeStreams(scaleStreams, 2)),
		"4b multi-scale k=3": fuse.NegLog10(fuse.AgreeStreams(scaleStreams, 3)),
	}

	period, strength := prep.DetectPeriod(v, prep.Options{})
	copt := conformal.StreamOptions{Alpha: 0.005, Calibration: conformalCalibration(len(v))}
	if strength >= 0.3 && period >= 4 && len(v) >= 8*period {
		copt.Period = period
	}
	out["5 +conformal"] = conformal.Scores(out["4 +multi-scale"], copt)
	return out
}

var stackOrder = []string{
	"1 fisher(raw)", "2 +deseasonalized", "3 +agreement k=2", "3b agreement k=3",
	"4 +multi-scale", "4b multi-scale k=3", "5 +conformal",
}

func buildStackCache(series []CorpusSeries) map[string]map[string][]float64 {
	out := make(map[string]map[string][]float64, len(series))
	var mu sync.Mutex
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i := range series {
		wg.Add(1)
		go func(s CorpusSeries) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			m := stackScores(s)
			mu.Lock()
			out[s.Key] = m
			mu.Unlock()
		}(series[i])
	}
	wg.Wait()
	return out
}

func cachedStack(cache map[string]map[string][]float64, name string) ScoreFunc {
	return func(s CorpusSeries) []float64 {
		m, ok := cache[s.Key]
		if !ok {
			return nil
		}
		return m[name]
	}
}

func evtThresholdAt(q float64) ThresholdFunc {
	return func(_ CorpusSeries, scores []float64) float64 {
		z, _, ok := evt.POT(scores, evt.Options{Q: q, Level: 0.98})
		if !ok {
			return QuantileThreshold(0.999)(CorpusSeries{}, scores)
		}
		return z
	}
}

func conformalThresholdAt(alpha float64) ThresholdFunc {
	return func(_ CorpusSeries, _ []float64) float64 { return -math.Log10(alpha) }
}

func thresholdFor(stackName string, level float64) ThresholdFunc {
	if stackName == "5 +conformal" {
		return conformalThresholdAt(level)
	}
	return evtThresholdAt(level)
}

func alarmAt(thr ThresholdFunc, opt guard.Options, warmup func(int) int) AlarmFunc {
	return func(s CorpusSeries, scores []float64) []bool {
		o := opt
		o.Threshold = thr(s, scores)
		o.Warmup = warmup(len(scores))
		return guard.Apply(scores, o)
	}
}

func warmupForStack(name string) func(int) int {
	if name == "5 +conformal" {
		return conformalCalibration
	}
	return calibrationFor
}

type policy struct {
	name string
	opt  guard.Options
}

var policies = []policy{
	{"raw", guard.Options{}},
	{"refractory", guard.Options{Refractory: 60}},
	{"2of10", guard.Options{Persist: 2, Of: 10}},
	{"2of10+refr", guard.Options{Persist: 2, Of: 10, Refractory: 60}},
	{"3of10+refr", guard.Options{Persist: 3, Of: 10, Refractory: 60}},
	{"3of5+refr", guard.Options{Persist: 3, Of: 5, Refractory: 60}},
}

func TestPrecisionStack(t *testing.T) {
	series := corpusFromEnv(t)
	start := time.Now()
	cache := buildStackCache(series)
	t.Logf("corpus: %d series, stack cache built in %v", len(series), time.Since(start).Round(time.Millisecond))

	levels := []float64{1e-2, 1e-3, 1e-4}
	var all []Operating
	for _, name := range stackOrder {
		fn := cachedStack(cache, name)
		warm := warmupForStack(name)
		ls := levels
		if name == "5 +conformal" {
			ls = []float64{0.05, 0.005, 0.0005}
		}
		for _, level := range ls {
			for _, p := range policies {
				label := fmt.Sprintf("%s | %g | %s", name, level, p.name)
				all = append(all, EventScore(series, fn, alarmAt(thresholdFor(name, level), p.opt, warm), label))
			}
		}
	}

	matched := make(map[string]Operating)
	for _, op := range all {
		var stack, rest string
		fmt.Sscanf(op.Label, "%s", &stack)
		for _, name := range stackOrder {
			if len(op.Label) >= len(name) && op.Label[:len(name)] == name {
				rest = op.Label[len(name):]
				stack = name
				break
			}
		}
		if rest == " | 0.001 | raw" {
			matched[stack] = op
		}
	}
	fmt.Printf("\nsame threshold (q=1e-3), no alarm policy — what each stage buys on its own:\n")
	fmt.Printf("%-20s %8s %8s %8s %9s\n", "stack", "recall", "prec", "F1", "alarms/s")
	for _, name := range stackOrder {
		if op, ok := matched[name]; ok {
			fmt.Printf("%-20s %8.4f %8.4f %8.4f %9.1f\n", name, op.EventRecall, op.AlarmPrecision, op.F1, op.AlarmsPerSerie)
		}
	}

	pareto := frontier(all)
	fmt.Printf("\nPareto frontier over %d configurations (recall vs alarm precision):\n", len(all))
	fmt.Printf("%-46s %8s %8s %8s %9s\n", "configuration", "recall", "prec", "F1", "alarms/s")
	for _, op := range pareto {
		fmt.Printf("%-46s %8.4f %8.4f %8.4f %9.1f\n", op.Label, op.EventRecall, op.AlarmPrecision, op.F1, op.AlarmsPerSerie)
	}

	bestF1, bestPrec := Operating{}, Operating{}
	for _, op := range all {
		if op.F1 > bestF1.F1 {
			bestF1 = op
		}
		if op.EventRecall >= 0.08 && op.AlarmPrecision > bestPrec.AlarmPrecision {
			bestPrec = op
		}
	}
	fmt.Printf("\nbest event F1:        %-40s R=%.4f P=%.4f F1=%.4f\n", bestF1.Label, bestF1.EventRecall, bestF1.AlarmPrecision, bestF1.F1)
	fmt.Printf("best precision (R>=8%%): %-38s R=%.4f P=%.4f F1=%.4f\n", bestPrec.Label, bestPrec.EventRecall, bestPrec.AlarmPrecision, bestPrec.F1)

	plain := matched["1 fisher(raw)"]
	if plain.Series == 0 {
		t.Fatal("the plain baseline did not run")
	}
	for _, c := range []struct {
		name  string
		floor float64
	}{
		{"1 fisher(raw)", 0.40},
		{"2 +deseasonalized", 0.44},
		{"3 +agreement k=2", 0.51},
		{"4 +multi-scale", 0.51},
	} {
		op, ok := matched[c.name]
		if !ok {
			t.Errorf("%s did not produce a matched-threshold result", c.name)
			continue
		}
		if op.F1 < c.floor {
			t.Errorf("%s regressed below its event-F1 floor: %.4f < %.4f", c.name, op.F1, c.floor)
		}
	}
	if matched["2 +deseasonalized"].F1 <= plain.F1 {
		t.Errorf("deseasonalizing must beat the raw ensemble at a matched threshold: %.4f vs %.4f",
			matched["2 +deseasonalized"].F1, plain.F1)
	}
	if matched["3 +agreement k=2"].F1 <= matched["2 +deseasonalized"].F1 {
		t.Errorf("calibrated agreement must beat plain Fisher: %.4f vs %.4f",
			matched["3 +agreement k=2"].F1, matched["2 +deseasonalized"].F1)
	}
	if bestF1.F1 <= plain.F1 {
		t.Errorf("the stack must beat the plain ensemble on event F1: %.4f vs %.4f", bestF1.F1, plain.F1)
	}
	if bestF1.F1 < 0.53 {
		t.Errorf("best event F1 regressed: %.4f < 0.53 (%s)", bestF1.F1, bestF1.Label)
	}
	if bestPrec.AlarmPrecision < 0.70 {
		t.Errorf("some configuration with >=8%% recall must clear 70%% alarm precision, best was %.4f (%s)",
			bestPrec.AlarmPrecision, bestPrec.Label)
	}

	var clean Operating
	for _, op := range all {
		if op.AlarmPrecision >= 0.95 && op.EventRecall > clean.EventRecall {
			clean = op
		}
	}
	if clean.Alarms == 0 {
		t.Error("no configuration reached 95% alarm precision — the paranoid end of the frontier is gone")
	} else {
		fmt.Printf("quietest safe point:  %-40s R=%.4f P=%.4f (%.2f alarms/series)\n",
			clean.Label, clean.EventRecall, clean.AlarmPrecision, clean.AlarmsPerSerie)
		if clean.EventRecall < 0.03 {
			t.Errorf("the 95%%-precision point must still catch something, got recall %.4f", clean.EventRecall)
		}
	}
	if len(pareto) < 6 {
		t.Errorf("the frontier collapsed to %d points — the knobs are not trading off", len(pareto))
	}
}

func frontier(ops []Operating) []Operating {
	var out []Operating
	for _, a := range ops {
		if a.Alarms == 0 {
			continue
		}
		dominated := false
		for _, b := range ops {
			if b.EventRecall >= a.EventRecall && b.AlarmPrecision >= a.AlarmPrecision &&
				(b.EventRecall > a.EventRecall || b.AlarmPrecision > a.AlarmPrecision) {
				dominated = true
				break
			}
		}
		if !dominated {
			out = append(out, a)
		}
	}
	sortOperating(out)
	return out
}

func sortOperating(ops []Operating) {
	for i := 1; i < len(ops); i++ {
		for j := i; j > 0 && ops[j].EventRecall < ops[j-1].EventRecall; j-- {
			ops[j], ops[j-1] = ops[j-1], ops[j]
		}
	}
}
