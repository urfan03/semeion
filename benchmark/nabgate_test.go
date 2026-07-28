package benchmark

import (
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/urfan03/semeion/engine"
	"github.com/urfan03/semeion/evt"
	"github.com/urfan03/semeion/fuse"
	"github.com/urfan03/semeion/hst"
	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/mp"
	"github.com/urfan03/semeion/selector"
	"github.com/urfan03/semeion/sub"
)

func corpusFromEnv(t *testing.T) []CorpusSeries {
	t.Helper()
	if dir := os.Getenv("SEMEION_UCR_DIR"); dir != "" {
		series, err := LoadUCRCorpus(dir)
		if err != nil {
			t.Fatal(err)
		}
		return series
	}
	dir := os.Getenv("SEMEION_NAB_DIR")
	if dir == "" {
		t.Skip("set SEMEION_NAB_DIR (or SEMEION_UCR_DIR) to run the corpus gate")
	}
	series, err := LoadCorpusRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	return series
}

func seriesInterval(s CorpusSeries) time.Duration {
	if len(s.Points) < 2 {
		return time.Minute
	}
	deltas := make([]time.Duration, 0, len(s.Points)-1)
	for i := 1; i < len(s.Points); i++ {
		if d := s.Points[i].Time.Sub(s.Points[i-1].Time); d > 0 {
			deltas = append(deltas, d)
		}
	}
	if len(deltas) == 0 {
		return time.Minute
	}
	sort.Slice(deltas, func(i, j int) bool { return deltas[i] < deltas[j] })
	return deltas[len(deltas)/2]
}

func engineScores(s CorpusSeries) []float64 {
	span := seriesInterval(s)
	job := jobspec.Job{Name: "nab", BucketSpan: span,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "value", Side: jobspec.SideBoth}}}
	eng, err := engine.New(job)
	if err != nil {
		return nil
	}
	byTime := make(map[time.Time]float64)
	for _, br := range eng.Run(s.Points, 0) {
		byTime[br.Time] = br.Score
	}
	out := make([]float64, len(s.Points))
	for i, p := range s.Points {
		out[i] = byTime[p.Time.Truncate(span)]
	}
	return out
}

func randomScores(s CorpusSeries) []float64 {
	rng := rand.New(rand.NewPCG(uint64(len(s.Points)), 0xbadc0ffee))
	out := make([]float64, len(s.Points))
	for i := range out {
		out[i] = rng.Float64()
	}
	return out
}

func mpScores(s CorpusSeries) []float64 { return mp.Scores(s.Values(), mp.Options{}) }

func dampScores(s CorpusSeries) []float64 { return mp.DAMP(s.Values(), mp.DAMPOptions{}) }

func hstScores(s CorpusSeries) []float64 {
	return hst.Series(s.Values(), hst.SeriesOptions{})
}

func subOptions() sub.Options { return sub.Options{Window: 16, K: 5} }

func knnScores(s CorpusSeries) []float64 { return sub.KNN(s.Values(), subOptions()) }

func lofScores(s CorpusSeries) []float64 { return sub.LOF(s.Values(), subOptions()) }

func pcaScores(s CorpusSeries) []float64 {
	return sub.PCA(s.Values(), sub.PCAOptions{Options: subOptions(), Variance: 0.9})
}

func iforestScores(s CorpusSeries) []float64 {
	return sub.IForest(s.Values(), sub.ForestOptions{Options: subOptions(), Trees: 100, SampleSize: 256, Seed: 11})
}

func evtStreamOptions(n int) evt.StreamOptions {
	c := n / 5
	if c < 200 {
		c = 200
	}
	if c > 800 {
		c = 800
	}
	return evt.StreamOptions{Calibration: c, Drift: true}
}

func evtProbabilities(s CorpusSeries) []float64 {
	v := s.Values()
	return evt.TwoSidedProbabilities(v, evtStreamOptions(len(v)))
}

func evtScores(s CorpusSeries) []float64 { return fuse.NegLog10(evtProbabilities(s)) }

func evtThreshold(_ CorpusSeries, scores []float64) float64 {
	z, _, ok := evt.POT(scores, evt.Options{Q: 1e-3, Level: 0.98})
	if !ok {
		return math.Inf(1)
	}
	return z
}

var baseDetectors = []struct {
	name string
	fn   ScoreFunc
}{
	{"mp", mpScores},
	{"damp", dampScores},
	{"hst", hstScores},
	{"evt", evtScores},
	{"sub-knn", knnScores},
	{"sub-lof", lofScores},
	{"sub-pca", pcaScores},
	{"sub-iforest", iforestScores},
}

type cached struct {
	scores map[string][]float64
	pvals  map[string][]float64
}

func warmup(n int) int {
	w := n / 10
	if w < 50 {
		w = 50
	}
	return w
}

func buildCache(series []CorpusSeries) []cached {
	out := make([]cached, len(series))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i := range series {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			s := series[i]
			c := cached{scores: make(map[string][]float64), pvals: make(map[string][]float64)}
			c.pvals["evt"] = evtProbabilities(s)
			c.scores["evt"] = fuse.NegLog10(c.pvals["evt"])
			for _, d := range baseDetectors {
				if d.name == "evt" {
					continue
				}
				sc := d.fn(s)
				c.scores[d.name] = sc
				c.pvals[d.name] = fuse.PValues(sc, warmup(len(s.Points)))
			}
			out[i] = c
		}(i)
	}
	wg.Wait()
	return out
}

func cachedFunc(series []CorpusSeries, cache []cached, name string) ScoreFunc {
	index := make(map[string]int, len(series))
	for i, s := range series {
		index[s.Key] = i
	}
	return func(s CorpusSeries) []float64 {
		i, ok := index[s.Key]
		if !ok {
			return nil
		}
		return cache[i].scores[name]
	}
}

func fisherFunc(series []CorpusSeries, cache []cached, names []string) ScoreFunc {
	index := make(map[string]int, len(series))
	for i, s := range series {
		index[s.Key] = i
	}
	return func(s CorpusSeries) []float64 {
		i, ok := index[s.Key]
		if !ok {
			return nil
		}
		streams := make([][]float64, 0, len(names))
		for _, n := range names {
			streams = append(streams, cache[i].pvals[n])
		}
		return fuse.NegLog10(fuse.FisherStreams(streams))
	}
}

func weightedFunc(series []CorpusSeries, cache []cached, names []string) ScoreFunc {
	index := make(map[string]int, len(series))
	for i, s := range series {
		index[s.Key] = i
	}
	return func(s CorpusSeries) []float64 {
		i, ok := index[s.Key]
		if !ok {
			return nil
		}
		streams := make([][]float64, 0, len(names))
		for _, n := range names {
			streams = append(streams, cache[i].pvals[n])
		}
		combined, _ := fuse.WeightedCombine(streams, fuse.WeightedOptions{Warmup: warmup(len(s.Points))})
		return fuse.NegLog10(combined)
	}
}

func selectorLOOFunc(series []CorpusSeries, cache []cached, names []string) (ScoreFunc, map[string]int) {
	index := make(map[string]int, len(series))
	for i, s := range series {
		index[s.Key] = i
	}
	full := selector.New(3, names[0])
	for i, s := range series {
		if s.Anomalies == 0 {
			continue
		}
		best, bestScore := names[0], -1.0
		for _, n := range names {
			if v := AUCPR(cache[i].scores[n], s.Labels); v > bestScore {
				best, bestScore = n, v
			}
		}
		full.Add(s.Key, selector.Extract(s.Values()), best)
	}
	full.Fit()

	picks := make(map[string]int)
	fn := func(s CorpusSeries) []float64 {
		i, ok := index[s.Key]
		if !ok {
			return nil
		}
		choice := full.Without(s.Key).Predict(selector.Extract(s.Values()))
		picks[choice]++
		return cache[i].scores[choice]
	}
	return fn, picks
}

func gateOptions() CorpusOptions {
	return CorpusOptions{
		VUSBuffer: -1,
		PAK:       0.2,
		Range:     RangeOptions{Alpha: 0.5, Bias: BiasFlat},
		Threshold: evtThreshold,
	}
}

type gateRow struct {
	name string
	sum  CorpusSummary
	took time.Duration
}

func runOne(t *testing.T, series []CorpusSeries, name string, fn ScoreFunc) gateRow {
	t.Helper()
	start := time.Now()
	_, sum := RunCorpusWith(series, fn, gateOptions())
	row := gateRow{name, sum, time.Since(start)}
	t.Logf("%-20s PA-F1=%.4f PA20-F1=%.4f range-F1=%.4f VUS-PR=%.4f VUS-ROC=%.4f AUC-PR=%.4f fixed-F1=%.4f %v",
		name, sum.MacroF1, sum.MacroPAKF1, sum.MacroRange, sum.MacroVUSPR, sum.MacroVUSROC,
		sum.MacroAUCPR, sum.MacroFixed, row.took.Round(time.Millisecond))
	return row
}

type floor struct {
	aucPR   float64
	rangeF1 float64
	fixed   float64
	primary bool
}

var floors = map[string]floor{
	"hst":         {0.180, 0.480, 0.50, true},
	"evt":         {0.180, 0.420, 0.30, true},
	"fisher":      {0.190, 0.490, 0.45, true},
	"fisher-all":  {0.170, 0.440, 0.33, true},
	"selector":    {0.180, 0.440, 0.36, true},
	"mp":          {0.105, 0.320, 0.30, false},
	"damp":        {0.105, 0.320, 0.28, false},
	"sub-knn":     {0.115, 0.360, 0.24, false},
	"sub-lof":     {0.108, 0.330, 0.32, false},
	"sub-pca":     {0.105, 0.300, 0.20, false},
	"sub-iforest": {0.110, 0.330, 0.29, false},
	"weighted":    {0.150, 0.390, 0.29, false},
}

func TestNABCorpusGate(t *testing.T) {
	series := corpusFromEnv(t)
	t.Logf("corpus: %d series", len(series))

	base := runOne(t, series, "baseline-engine", engineScores)
	if base.sum.Scored == 0 {
		t.Fatal("no series were scored — corpus labels look wrong")
	}
	chance := runOne(t, series, "random", randomScores)

	cacheStart := time.Now()
	cache := buildCache(series)
	t.Logf("detector cache built in %v", time.Since(cacheStart).Round(time.Millisecond))

	names := make([]string, 0, len(baseDetectors))
	for _, d := range baseDetectors {
		names = append(names, d.name)
	}

	rows := make([]gateRow, 0, len(names)+4)
	for _, n := range names {
		rows = append(rows, runOne(t, series, n, cachedFunc(series, cache, n)))
	}
	rows = append(rows, runOne(t, series, "fisher", fisherFunc(series, cache, []string{"evt", "mp", "hst"})))
	rows = append(rows, runOne(t, series, "fisher-all", fisherFunc(series, cache, names)))
	rows = append(rows, runOne(t, series, "weighted", weightedFunc(series, cache, names)))

	selFn, picks := selectorLOOFunc(series, cache, names)
	rows = append(rows, runOne(t, series, "selector", selFn))
	t.Logf("selector picks (leave-one-out): %v", picks)

	fmt.Printf("\n%-20s %8s %8s %8s %8s %8s %8s\n", "detector", "PA-F1", "PA20-F1", "range-F1", "VUS-PR", "AUC-PR", "fixed")
	for _, r := range append([]gateRow{base, chance}, rows...) {
		fmt.Printf("%-20s %8.4f %8.4f %8.4f %8.4f %8.4f %8.4f\n", r.name,
			r.sum.MacroF1, r.sum.MacroPAKF1, r.sum.MacroRange, r.sum.MacroVUSPR, r.sum.MacroAUCPR, r.sum.MacroFixed)
	}

	t.Logf("a random scorer reaches PA-F1=%.4f and VUS-PR=%.4f on this corpus — neither metric is used to gate anything",
		chance.sum.MacroF1, chance.sum.MacroVUSPR)
	if chance.sum.MacroF1 < base.sum.MacroF1 {
		t.Errorf("random scored below the engine on PA-F1 (%.4f vs %.4f) — the corpus or the metric changed shape, re-derive the floors",
			chance.sum.MacroF1, base.sum.MacroF1)
	}

	for _, r := range rows {
		f, ok := floors[r.name]
		if !ok {
			t.Errorf("%s has no recorded floor", r.name)
			continue
		}
		if r.sum.Scored != base.sum.Scored {
			t.Errorf("%s scored %d series, baseline scored %d", r.name, r.sum.Scored, base.sum.Scored)
		}
		if r.sum.MacroAUCPR <= chance.sum.MacroAUCPR {
			t.Errorf("%s must beat a random scorer on AUC-PR: %.4f vs %.4f", r.name, r.sum.MacroAUCPR, chance.sum.MacroAUCPR)
		}
		if r.sum.MacroRange <= chance.sum.MacroRange {
			t.Errorf("%s must beat a random scorer on range-F1: %.4f vs %.4f", r.name, r.sum.MacroRange, chance.sum.MacroRange)
		}
		if r.sum.MacroFixed <= base.sum.MacroFixed {
			t.Errorf("%s must beat the engine at a fixed operating point: %.4f vs %.4f", r.name, r.sum.MacroFixed, base.sum.MacroFixed)
		}
		if f.primary {
			if r.sum.MacroAUCPR <= base.sum.MacroAUCPR {
				t.Errorf("%s is a primary detector and must beat the engine on AUC-PR: %.4f vs %.4f", r.name, r.sum.MacroAUCPR, base.sum.MacroAUCPR)
			}
			if r.sum.MacroRange <= base.sum.MacroRange {
				t.Errorf("%s is a primary detector and must beat the engine on range-F1: %.4f vs %.4f", r.name, r.sum.MacroRange, base.sum.MacroRange)
			}
		}
		if r.sum.MacroAUCPR < f.aucPR {
			t.Errorf("%s regressed below its AUC-PR floor: %.4f < %.4f", r.name, r.sum.MacroAUCPR, f.aucPR)
		}
		if r.sum.MacroRange < f.rangeF1 {
			t.Errorf("%s regressed below its range-F1 floor: %.4f < %.4f", r.name, r.sum.MacroRange, f.rangeF1)
		}
		if r.sum.MacroFixed < f.fixed {
			t.Errorf("%s regressed below its fixed-threshold floor: %.4f < %.4f", r.name, r.sum.MacroFixed, f.fixed)
		}
	}
}
