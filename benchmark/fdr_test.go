package benchmark

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/urfan03/semeion/fdr"
	"github.com/urfan03/semeion/fuse"
	"github.com/urfan03/semeion/guard"
	"github.com/urfan03/semeion/mp"
	"github.com/urfan03/semeion/prep"
	"github.com/urfan03/semeion/shape"
)

func residualStreams(values []float64, trim float64) [][]float64 {
	warm := warmupFor(len(values))
	return [][]float64{
		evtPOn(values),
		fuse.TrimmedPValues(mp.Scores(values, mp.Options{}), warm, trim),
		fuse.TrimmedPValues(hstOn(values), warm, trim),
	}
}

type pKey struct {
	key  string
	trim float64
}

var (
	agreeMu    sync.Mutex
	agreeCache = map[pKey][]float64{}
)

func agreeP(s CorpusSeries, trim float64) []float64 {
	k := pKey{s.Key, trim}
	agreeMu.Lock()
	if v, ok := agreeCache[k]; ok {
		agreeMu.Unlock()
		return v
	}
	agreeMu.Unlock()

	resid, _ := prep.Deseasonalize(s.Values(), prep.Options{})
	v := fuse.AgreeStreams(residualStreams(resid, trim), 2)

	agreeMu.Lock()
	agreeCache[k] = v
	agreeMu.Unlock()
	return v
}

func warmAgreeCache(series []CorpusSeries, trims []float64) {
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i := range series {
		for _, trim := range trims {
			wg.Add(1)
			go func(s CorpusSeries, trim float64) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				agreeP(s, trim)
			}(series[i], trim)
		}
	}
	wg.Wait()
}

func fdrAlarms(q float64, opt guard.Options, trim float64) AlarmFunc {
	return func(s CorpusSeries, _ []float64) []bool {
		p := agreeP(s, trim)
		if p == nil {
			return nil
		}
		raw := fdr.OnlineFrom(p, q, calibrationFor(len(p)))
		if opt.Persist <= 1 && opt.Refractory <= 0 {
			return raw
		}
		scores := make([]float64, len(raw))
		for i, r := range raw {
			if r {
				scores[i] = 1
			}
		}
		o := opt
		o.Threshold = 1
		return guard.Apply(scores, o)
	}
}

func batchFDRAlarms(q float64, trim float64) AlarmFunc {
	return func(s CorpusSeries, _ []float64) []bool {
		p := agreeP(s, trim)
		if p == nil {
			return nil
		}
		warm := calibrationFor(len(p))
		if warm >= len(p) {
			return make([]bool, len(p))
		}
		_, rej := fdr.StoreyBH(p[warm:], q, 0.5)
		out := make([]bool, len(p))
		copy(out[warm:], rej)
		return out
	}
}

func budgetAlarms(b guard.Budget, opt guard.Options, trim float64) AlarmFunc {
	return func(s CorpusSeries, _ []float64) []bool {
		p := agreeP(s, trim)
		if p == nil {
			return nil
		}
		scores := fuse.NegLog10(p)
		o := opt
		o.Warmup = calibrationFor(len(scores))
		return guard.WithBudget(scores, b, o)
	}
}

type eventFDR struct {
	alpha    float64
	q        float64
	gap      int
	peakOnly bool
	by       bool
	trim     float64
}

func (e eventFDR) alarms(series []CorpusSeries) map[string][]bool {
	type entry struct {
		key   string
		cands []guard.Candidate
		n     int
	}
	var all []entry
	var pooled []float64
	for _, s := range series {
		p := agreeP(s, e.trim)
		if p == nil {
			continue
		}
		warm := calibrationFor(len(p))
		if warm >= len(p) {
			continue
		}
		masked := append([]float64(nil), p...)
		for i := 0; i < warm; i++ {
			masked[i] = 1
		}
		cands := guard.CandidatesFromP(masked, e.alpha, e.gap)
		all = append(all, entry{s.Key, cands, len(p)})
		for _, c := range cands {
			pooled = append(pooled, guard.SidakP(c.MinP, c.Length))
		}
	}
	if len(pooled) == 0 {
		return nil
	}

	var reject []bool
	if e.by {
		_, reject = fdr.BY(pooled, e.q)
	} else {
		_, reject = fdr.StoreyBH(pooled, e.q, 0.5)
	}

	out := make(map[string][]bool, len(all))
	at := 0
	for _, en := range all {
		accept := make([]bool, len(en.cands))
		for i := range en.cands {
			accept[i] = reject[at]
			at++
		}
		out[en.key] = guard.Mask(en.n, en.cands, accept, e.peakOnly)
	}
	return out
}

func shapeGated(alpha float64, gap, minDuration int, persistentOnly bool, opt guard.Options) AlarmFunc {
	return func(s CorpusSeries, _ []float64) []bool {
		p := agreeP(s, 0)
		if p == nil {
			return nil
		}
		warm := calibrationFor(len(p))
		masked := append([]float64(nil), p...)
		for i := 0; i < warm && i < len(masked); i++ {
			masked[i] = 1
		}
		cands := guard.CandidatesFromP(masked, alpha, gap)
		values := s.Values()
		accept := make([]bool, len(cands))
		for i, c := range cands {
			if c.Length < minDuration {
				continue
			}
			cls := shape.Classify(values, c.Start, c.End, shape.Options{Context: 200})
			if cls.Kind == shape.Unknown {
				continue
			}
			if persistentOnly && !shape.Persistent(cls.Kind) {
				continue
			}
			accept[i] = true
		}
		out := guard.Mask(len(p), cands, accept, false)
		if opt.Refractory <= 0 {
			return out
		}
		marks := make([]float64, len(out))
		for i, v := range out {
			if v {
				marks[i] = 1
			}
		}
		o := opt
		o.Threshold = 1
		return guard.Apply(marks, o)
	}
}

func effectGated(inner AlarmFunc, minRel float64) AlarmFunc {
	return func(s CorpusSeries, scores []float64) []bool {
		alarms := inner(s, scores)
		if alarms == nil {
			return nil
		}
		v := s.Values()
		base, scale := guard.RollingBaseline(v, 100)
		return guard.GateByEffect(alarms, guard.Effect{
			Values: v, Baseline: base, Scale: scale, MinRel: minRel,
		})
	}
}

func TestFDRPrecisionTarget(t *testing.T) {
	series := corpusFromEnv(t)
	start := time.Now()
	warmAgreeCache(series, []float64{0, 0.02})
	t.Logf("p-value cache built in %v", time.Since(start).Round(time.Millisecond))

	base := func(s CorpusSeries) []float64 { return fuse.NegLog10(agreeP(s, 0)) }

	type run struct {
		label string
		fn    AlarmFunc
	}
	runs := []run{
		{"evt q=1e-3 (reference)", alarmAt(evtThresholdAt(1e-3), guard.Sensitive(), calibrationFor)},
		{"evt q=1e-3 +precise", alarmAt(evtThresholdAt(1e-3), guard.Precise(), calibrationFor)},
	}
	for _, q := range []float64{0.05, 0.1, 0.2, 0.3} {
		runs = append(runs,
			run{fmt.Sprintf("online FDR q=%g", q), fdrAlarms(q, guard.Sensitive(), 0)},
			run{fmt.Sprintf("online FDR q=%g +refr", q), fdrAlarms(q, guard.Options{Refractory: 60}, 0)},
			run{fmt.Sprintf("batch FDR q=%g", q), batchFDRAlarms(q, 0)},
		)
	}
	runs = append(runs,
		run{"online FDR q=0.2 trim=2%", fdrAlarms(0.2, guard.Sensitive(), 0.02)},
		run{"online FDR q=0.2 +effect", effectGated(fdrAlarms(0.2, guard.Sensitive(), 0), 3)},
		run{"online FDR q=0.2 +refr+effect", effectGated(fdrAlarms(0.2, guard.Options{Refractory: 60}, 0), 3)},
		run{"budget 2/1000", budgetAlarms(guard.Budget{Alarms: 2, Per: 1000}, guard.Sensitive(), 0)},
		run{"budget 1/1000", budgetAlarms(guard.Budget{Alarms: 1, Per: 1000}, guard.Sensitive(), 0)},
		run{"budget 1/1000 +precise", budgetAlarms(guard.Budget{Alarms: 1, Per: 1000}, guard.Precise(), 0)},
		run{"budget 1/2000 +effect", effectGated(budgetAlarms(guard.Budget{Alarms: 1, Per: 2000}, guard.Sensitive(), 0), 3)},
	)
	for _, minDur := range []int{3, 5, 10, 20} {
		runs = append(runs,
			run{fmt.Sprintf("shape dur>=%d", minDur), shapeGated(1e-3, 10, minDur, false, guard.Options{})},
			run{fmt.Sprintf("shape dur>=%d +refr", minDur), shapeGated(1e-3, 10, minDur, false, guard.Options{Refractory: 60})},
		)
	}
	runs = append(runs,
		run{"shape dur>=5 persistent", shapeGated(1e-3, 10, 5, true, guard.Options{})},
		run{"shape dur>=10 persistent", shapeGated(1e-3, 10, 10, true, guard.Options{})},
		run{"shape a=1e-2 dur>=10", shapeGated(1e-2, 10, 10, false, guard.Options{})},
		run{"shape a=1e-2 dur>=20", shapeGated(1e-2, 10, 20, false, guard.Options{})},
		run{"shape dur>=10 +effect", effectGated(shapeGated(1e-3, 10, 10, false, guard.Options{}), 3)},
	)

	for _, alpha := range []float64{1e-2, 1e-3} {
		for _, q := range []float64{0.05, 0.1, 0.2} {
			for _, by := range []bool{false, true} {
				for _, peak := range []bool{false, true} {
					e := eventFDR{alpha: alpha, q: q, gap: 10, peakOnly: peak, by: by}
					precomputed := e.alarms(series)
					name := "event"
					if by {
						name = "eventBY"
					}
					mode := "run"
					if peak {
						mode = "peak"
					}
					label := fmt.Sprintf("%s a=%g q=%g %s", name, alpha, q, mode)
					runs = append(runs, run{label, func(s CorpusSeries, _ []float64) []bool {
						return precomputed[s.Key]
					}})
				}
			}
		}
	}

	fmt.Printf("\n%-32s %8s %8s %8s %9s\n", "configuration", "recall", "prec", "F1", "alarms/s")
	var ops []Operating
	for _, r := range runs {
		op := EventScore(series, base, r.fn, r.label)
		ops = append(ops, op)
		fmt.Printf("%-32s %8.4f %8.4f %8.4f %9.2f\n", r.label, op.EventRecall, op.AlarmPrecision, op.F1, op.AlarmsPerSerie)
	}

	var reference Operating
	best80 := Operating{}
	bestF1 := Operating{}
	for _, op := range ops {
		if op.Label == "evt q=1e-3 (reference)" {
			reference = op
		}
		if op.AlarmPrecision >= 0.80 && op.EventRecall > best80.EventRecall {
			best80 = op
		}
		if op.F1 > bestF1.F1 {
			bestF1 = op
		}
	}

	fmt.Printf("\nreference (EVT q=1e-3):   R=%.4f P=%.4f\n", reference.EventRecall, reference.AlarmPrecision)
	if best80.Alarms > 0 {
		fmt.Printf("best >=80%% precision:     %-30s R=%.4f P=%.4f (%.2f alarms/series)\n",
			best80.Label, best80.EventRecall, best80.AlarmPrecision, best80.AlarmsPerSerie)
	} else {
		fmt.Printf("best >=80%% precision:     none reached 80%%\n")
	}
	fmt.Printf("best event F1:            %-30s R=%.4f P=%.4f F1=%.4f\n",
		bestF1.Label, bestF1.EventRecall, bestF1.AlarmPrecision, bestF1.F1)

	var nominal, worst Operating
	for _, op := range ops {
		if op.Label == "online FDR q=0.05" {
			nominal = op
		}
	}
	worst = nominal
	if worst.Alarms > 0 && worst.AlarmPrecision < 0.9 {
		fmt.Printf("\nFDR does NOT hold its nominal level on this data: q=0.05 should give ~95%% precision,\n")
		fmt.Printf("measured %.4f. The p-values are not valid here — the three detectors read the same\n", worst.AlarmPrecision)
		fmt.Printf("series so they are strongly dependent, the empirical null is contaminated by the\n")
		fmt.Printf("anomalies it is estimated from, and consecutive points are autocorrelated. Restructuring\n")
		fmt.Printf("the procedure (online, batch, event-level, Benjamini-Yekutieli) does not fix an invalid\n")
		fmt.Printf("input. Valid p-values need a curated known-normal calibration window.\n")
	}

	if reference.Alarms == 0 {
		t.Fatal("the reference configuration did not fire")
	}
	var bestPrec, bestAtRecall Operating
	for _, op := range ops {
		if op.EventRecall >= 0.08 && op.AlarmPrecision > bestPrec.AlarmPrecision {
			bestPrec = op
		}
		if op.EventRecall >= 0.40 && op.AlarmPrecision > bestAtRecall.AlarmPrecision {
			bestAtRecall = op
		}
	}
	fmt.Printf("best precision at >=40%% recall: %-28s R=%.4f P=%.4f (%.2f alarms/series)\n",
		bestAtRecall.Label, bestAtRecall.EventRecall, bestAtRecall.AlarmPrecision, bestAtRecall.AlarmsPerSerie)

	if bestPrec.AlarmPrecision <= reference.AlarmPrecision {
		t.Errorf("some configuration must beat the plain EVT threshold on precision: %.4f vs %.4f",
			bestPrec.AlarmPrecision, reference.AlarmPrecision)
	}
	if bestPrec.AlarmPrecision < 0.70 {
		t.Errorf("expected a >=70%% precision point at usable recall, best was %.4f (%s)",
			bestPrec.AlarmPrecision, bestPrec.Label)
	}
	if bestAtRecall.AlarmPrecision <= reference.AlarmPrecision {
		t.Errorf("the budget and effect gates must raise precision without giving up 40%% recall: %.4f vs %.4f",
			bestAtRecall.AlarmPrecision, reference.AlarmPrecision)
	}
	if bestAtRecall.AlarmPrecision < 0.60 {
		t.Errorf("precision at >=40%% recall regressed: %.4f < 0.60 (%s)",
			bestAtRecall.AlarmPrecision, bestAtRecall.Label)
	}
	if bestAtRecall.AlarmsPerSerie > 4 {
		t.Errorf("that operating point should also be quiet, got %.2f alarms/series", bestAtRecall.AlarmsPerSerie)
	}

	byLabel := make(map[string]Operating, len(ops))
	for _, op := range ops {
		byLabel[op.Label] = op
	}
	short, long := byLabel["shape dur>=3"], byLabel["shape dur>=20"]
	if short.Alarms == 0 || long.Alarms == 0 {
		t.Fatal("the duration sweep did not run")
	}
	if long.AlarmPrecision <= short.AlarmPrecision {
		t.Errorf("requiring a longer anomaly must raise precision: %.4f at dur>=20 vs %.4f at dur>=3",
			long.AlarmPrecision, short.AlarmPrecision)
	}
	if long.EventRecall >= short.EventRecall {
		t.Errorf("requiring a longer anomaly must cost recall: %.4f vs %.4f", long.EventRecall, short.EventRecall)
	}
}
