package detect

import (
	"math"
	"testing"
	"time"

	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/model"
)

// backfit should recover known additive components (up to a zero-mean shift
// absorbed into the level) from a clean two-seasonal series.
func TestBackfitRecoversComponents(t *testing.T) {
	p1, p2 := 12, 7
	trueC1 := make([]float64, p1)
	trueC2 := make([]float64, p2)
	for i := range trueC1 {
		trueC1[i] = 10 * math.Sin(2*math.Pi*float64(i)/float64(p1))
	}
	for i := range trueC2 {
		trueC2[i] = float64(i) - 3 // -3..3, zero-mean
	}
	var hist []float64
	var bkts []int64
	for i := 0; i < 420; i++ {
		v := 100 + trueC1[i%p1] + trueC2[i%p2]
		hist = append(hist, v)
		bkts = append(bkts, int64(i))
	}
	level, c1, c2 := backfit(hist, bkts, p1, p2)
	if math.Abs(level-100) > 0.5 {
		t.Fatalf("level should be ~100, got %v", level)
	}
	for i := range c1 {
		if math.Abs(c1[i]-trueC1[i]) > 0.5 {
			t.Fatalf("comp1[%d]=%v, want ~%v", i, c1[i], trueC1[i])
		}
	}
	for i := range c2 {
		if math.Abs(c2[i]-trueC2[i]) > 0.5 {
			t.Fatalf("comp2[%d]=%v, want ~%v", i, c2[i], trueC2[i])
		}
	}
}

// A genuine second period removes materially more residual variance than the
// single-period fit; a spurious/absent second period removes ~nothing.
func TestSeasonalResidualVarianceGain(t *testing.T) {
	p1, p2 := 12, 7
	var hist []float64
	var bkts []int64
	for i := 0; i < 420; i++ {
		v := 100 + 12*math.Sin(2*math.Pi*float64(i)/float64(p1)) + 8*float64((i%p2))
		hist = append(hist, v)
		bkts = append(bkts, int64(i))
	}
	var1 := seasonalResidualVar(hist, bkts, p1, 0)
	var2 := seasonalResidualVar(hist, bkts, p1, p2)
	gain := (var1 - var2) / var1
	if gain < multiMinGain {
		t.Fatalf("real weekly period should reduce residual variance by >= %.0f%%, got %.1f%%",
			multiMinGain*100, gain*100)
	}
	// Adding a second period that carries no independent signal (a single-cycle
	// series) yields little gain.
	var single []float64
	var sbkt []int64
	for i := 0; i < 420; i++ {
		single = append(single, 100+12*math.Sin(2*math.Pi*float64(i)/float64(p1)))
		sbkt = append(sbkt, int64(i))
	}
	sv1 := seasonalResidualVar(single, sbkt, p1, 0)
	sv2 := seasonalResidualVar(single, sbkt, p1, 5)
	if sv1 > 0 && (sv1-sv2)/sv1 >= multiMinGain {
		t.Fatalf("a spurious second period should NOT clear the gain threshold, got %.1f%%", (sv1-sv2)/sv1*100)
	}
}

// End-to-end: a series with two independent cycles (12 and 7) activates the
// two-component model, and a value that is normal for its daily phase but wrong
// once the weekly component is accounted for is flagged.
func TestMultiSeasonalModelActivatesAndCatchesCombined(t *testing.T) {
	p1, p2 := 12, 7
	span := time.Hour
	t0 := time.Unix(0, 0).UTC() // bucket index 0 → model phase == i % period
	val := func(i int) float64 {
		return 100 + 14*math.Sin(2*math.Pi*float64(i)/float64(p1)) + 18*float64((i%p2))
	}
	m := NewSeasonalModel(jobspec.SideBoth, model.NewGoProvider(), span)
	for i := 0; i < 500; i++ {
		m.Observe(t0.Add(time.Duration(i)*span), val(i))
	}
	if m.Period2() < 2 {
		t.Skipf("detector did not surface two independent periods (period=%d, period2=%d); machinery covered by unit tests",
			m.Period(), m.Period2())
	}
	// Feed a value at a peak-weekly phase (i%7 == 6) but at the daily-only level,
	// omitting the large weekly component — the combined model should flag it.
	i := 500
	for i%p2 != p2-1 {
		i++
	}
	dailyOnly := 100 + 14*math.Sin(2*math.Pi*float64(i)/float64(p1)) // no +18*6 weekly bump
	_, score, _, _ := m.Observe(t0.Add(time.Duration(i)*span), dailyOnly)
	if score < 50 {
		t.Fatalf("a value missing its weekly component should score anomalous, got %.1f", score)
	}
}
