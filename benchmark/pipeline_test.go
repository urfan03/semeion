package benchmark

import (
	"fmt"
	"testing"
	"time"

	"github.com/urfan03/semeion/guard"
	"github.com/urfan03/semeion/pipeline"
)

func pipelineAlarms(opt pipeline.Options) AlarmFunc {
	return func(s CorpusSeries, _ []float64) []bool {
		d, err := pipeline.New(opt)
		if err != nil {
			return nil
		}
		v := s.Values()
		out := make([]bool, len(v))
		for _, a := range d.Scan(v) {
			for i := a.Start; i <= a.End && i < len(out); i++ {
				out[i] = true
			}
		}
		return out
	}
}

func TestPipelineOnCorpus(t *testing.T) {
	series := corpusFromEnv(t)
	zero := func(s CorpusSeries) []float64 { return make([]float64, len(s.Points)) }

	base := pipeline.Options{History: 20000, Calibration: 800}
	configs := []struct {
		name string
		opt  pipeline.Options
	}{
		{"sensitive", withSens(base, pipeline.Sensitive)},
		{"balanced", withSens(base, pipeline.Balanced)},
		{"precise", withSens(base, pipeline.Precise)},
		{"paranoid", withSens(base, pipeline.Paranoid)},
		{"balanced +duration>=5", withDur(withSens(base, pipeline.Balanced), 5)},
		{"balanced +effect>=3", withEffect(withSens(base, pipeline.Balanced), 3)},
		{"balanced +budget 1/2000", withBudget(withSens(base, pipeline.Balanced), guard.Budget{Alarms: 1, Per: 2000})},
		{"balanced +budget +effect", withEffect(withBudget(withSens(base, pipeline.Balanced), guard.Budget{Alarms: 1, Per: 2000}), 3)},
		{"deseasonal +budget +effect", withDeseasonal(withEffect(withBudget(withSens(base, pipeline.Balanced), guard.Budget{Alarms: 1, Per: 2000}), 3))},
	}

	fmt.Printf("\nthe shipped pipeline, end to end, on %d series\n", len(series))
	fmt.Printf("%-28s %8s %8s %8s %10s %8s\n", "configuration", "recall", "prec", "F1", "pages/s", "time")
	var ops []Operating
	for _, c := range configs {
		start := time.Now()
		op := EventScore(series, zero, pipelineAlarms(c.opt), c.name)
		fmt.Printf("%-28s %8.4f %8.4f %8.4f %10.2f %8s\n",
			c.name, op.EventRecall, op.RegionPrecision, op.RegionF1, op.RegionsPerSerie,
			time.Since(start).Round(time.Millisecond))
		ops = append(ops, op)
	}

	var bestF1, bestPrec Operating
	for _, op := range ops {
		if op.RegionF1 > bestF1.RegionF1 {
			bestF1 = op
		}
		if op.EventRecall >= 0.20 && op.RegionPrecision > bestPrec.RegionPrecision {
			bestPrec = op
		}
	}
	fmt.Printf("\nbest F1:                  %-26s R=%.4f P=%.4f F1=%.4f\n",
		bestF1.Label, bestF1.EventRecall, bestF1.RegionPrecision, bestF1.RegionF1)
	fmt.Printf("best precision at R>=20%%: %-26s R=%.4f P=%.4f (%.2f pages/series)\n",
		bestPrec.Label, bestPrec.EventRecall, bestPrec.RegionPrecision, bestPrec.RegionsPerSerie)

	for _, op := range ops {
		if op.Series == 0 {
			t.Errorf("%s scored no series at all", op.Label)
		}
	}
	if bestF1.RegionF1 < 0.40 {
		t.Errorf("the shipped pipeline regressed on event F1: %.4f < 0.40 (%s)", bestF1.RegionF1, bestF1.Label)
	}
	if bestPrec.RegionPrecision < 0.55 {
		t.Errorf("the shipped pipeline must offer a >=55%% precision point at usable recall, best was %.4f (%s)",
			bestPrec.RegionPrecision, bestPrec.Label)
	}
	if bestPrec.RegionsPerSerie > 6 {
		t.Errorf("that point should also be quiet, got %.2f pages/series", bestPrec.RegionsPerSerie)
	}
}

func withSens(o pipeline.Options, s pipeline.Sensitivity) pipeline.Options {
	o.Sensitivity = s
	return o
}

func withDur(o pipeline.Options, d int) pipeline.Options {
	o.MinDuration = d
	return o
}

func withEffect(o pipeline.Options, e float64) pipeline.Options {
	o.MinEffect = e
	return o
}

func withBudget(o pipeline.Options, b guard.Budget) pipeline.Options {
	o.Budget = b
	return o
}

func withDeseasonal(o pipeline.Options) pipeline.Options {
	o.Deseasonal = true
	return o
}
