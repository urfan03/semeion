package engine

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func autoJob(auto *jobspec.AutoThreshold) jobspec.Job {
	return jobspec.Job{
		Name:          "auto",
		BucketSpan:    time.Minute,
		Detectors:     []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", Side: jobspec.SideBoth}},
		AutoThreshold: auto,
	}
}

func autoPoints(n int, spikes map[int]float64, seed uint64) []core.DataPoint {
	rng := rand.New(rand.NewPCG(seed, seed^0x99))
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pts := make([]core.DataPoint, n)
	for i := range pts {
		v := 100 + 5*math.Sin(float64(i)*0.3) + rng.NormFloat64()
		if mult, ok := spikes[i]; ok {
			v *= mult
		}
		pts[i] = core.DataPoint{Time: base.Add(time.Duration(i) * time.Minute), Value: v}
	}
	return pts
}

func TestAutoThresholdIsOffByDefault(t *testing.T) {
	eng, err := New(autoJob(nil))
	if err != nil {
		t.Fatal(err)
	}
	pts := autoPoints(800, map[int]float64{600: 3}, 1)
	plain, err := New(autoJob(nil))
	if err != nil {
		t.Fatal(err)
	}

	got := eng.Run(pts, 50)
	want := plain.Run(pts, 50)
	if len(got) != len(want) {
		t.Fatalf("bucket counts differ: %d vs %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Score != want[i].Score || len(got[i].Records) != len(want[i].Records) {
			t.Fatalf("a nil auto-threshold must change nothing, diverged at %d", i)
		}
	}
	if eng.AutoThresholdActive() {
		t.Fatal("no config means no auto threshold")
	}
	if eng.threshold != 50 {
		t.Fatalf("the configured threshold must stand, got %v", eng.threshold)
	}
}

func TestAutoThresholdLearnsAnOperatingPoint(t *testing.T) {
	cfg := &jobspec.AutoThreshold{Q: 1e-3, Calibration: 150, Min: 1, Max: 99}
	eng, err := New(autoJob(cfg))
	if err != nil {
		t.Fatal(err)
	}
	pts := autoPoints(1500, map[int]float64{1200: 4, 1400: 5}, 3)
	eng.Run(pts, 50)

	if !eng.AutoThresholdActive() {
		t.Fatal("the auto threshold should have calibrated over 1500 buckets")
	}
	if eng.threshold < 1 || eng.threshold > 99 {
		t.Fatalf("the learned threshold must respect the configured bounds, got %v", eng.threshold)
	}
	if eng.threshold == 50 {
		t.Fatal("the learned threshold should have moved off the configured default")
	}
}

func TestAutoThresholdRespectsBounds(t *testing.T) {
	cfg := &jobspec.AutoThreshold{Q: 1e-9, Calibration: 100, Min: 42, Max: 43}
	eng, err := New(autoJob(cfg))
	if err != nil {
		t.Fatal(err)
	}
	eng.Run(autoPoints(900, map[int]float64{700: 6}, 5), 50)
	if eng.threshold < 42 || eng.threshold > 43 {
		t.Fatalf("the threshold must be clamped into [42,43], got %v", eng.threshold)
	}
}

func TestAutoThresholdSurvivesSnapshot(t *testing.T) {
	cfg := &jobspec.AutoThreshold{Q: 1e-3, Calibration: 150}
	eng, err := New(autoJob(cfg))
	if err != nil {
		t.Fatal(err)
	}
	eng.Run(autoPoints(1000, nil, 7), 50)
	if !eng.AutoThresholdActive() {
		t.Fatal("expected the auto threshold to be live before snapshotting")
	}
	snap := eng.Snapshot()
	if len(snap.AutoThreshold) == 0 {
		t.Fatal("the snapshot must carry the auto-threshold state")
	}

	restored, err := New(autoJob(cfg))
	if err != nil {
		t.Fatal(err)
	}
	restored.Restore(snap)
	if !restored.AutoThresholdActive() {
		t.Fatal("restore must bring the auto threshold back live, not restart calibration")
	}
	if restored.threshold != eng.threshold {
		t.Fatalf("restored threshold differs: %v vs %v", restored.threshold, eng.threshold)
	}
}

func TestFeedbackComposesWithTheAutoThreshold(t *testing.T) {
	cfg := &jobspec.AutoThreshold{Q: 1e-3, Calibration: 150, Min: 1, Max: 99}
	spikes := map[int]float64{700: 1.2, 800: 1.3, 900: 1.5, 1000: 1.8, 1100: 2.2, 1200: 3, 1300: 5}
	pts := autoPoints(1500, spikes, 13)

	plain, err := New(autoJob(cfg))
	if err != nil {
		t.Fatal(err)
	}
	before := plain.Run(pts, 50)

	const reports = 5
	penalised, err := New(autoJob(cfg))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < reports; i++ {
		penalised.MarkFalsePositive("mean(v)", "")
	}
	after := penalised.Run(pts, 50)

	scores := func(res []core.BucketResult) []float64 {
		var out []float64
		for _, br := range res {
			for _, r := range br.Records {
				out = append(out, r.Score)
			}
		}
		return out
	}
	beforeScores, afterScores := scores(before), scores(after)
	if len(beforeScores) == 0 {
		t.Fatal("expected the unpenalised run to emit records")
	}
	if !penalised.AutoThresholdActive() {
		t.Fatal("the auto threshold must still be running alongside the feedback penalty")
	}
	if penalised.Threshold() != plain.Threshold() {
		t.Fatalf("feedback is per series and must not move the global learned threshold: %v vs %v",
			penalised.Threshold(), plain.Threshold())
	}

	bar := penalised.Threshold() + reports*fpPenaltyStep
	if bar > penalised.Threshold()+fpPenaltyMax {
		bar = penalised.Threshold() + fpPenaltyMax
	}
	weak := 0
	for _, s := range beforeScores {
		if s < bar {
			weak++
		}
	}
	if weak == 0 {
		t.Fatalf("fixture is useless: nothing sits between the learned threshold %.2f and the penalised bar %.2f",
			penalised.Threshold(), bar)
	}
	if len(afterScores) != len(beforeScores)-weak {
		t.Fatalf("feedback must drop exactly the records under the penalised bar: %d survived, expected %d",
			len(afterScores), len(beforeScores)-weak)
	}
	for _, s := range afterScores {
		if s < bar {
			t.Fatalf("a record scoring %.2f survived a bar of %.2f", s, bar)
		}
	}
}

func TestCalendarsSuppressUnderAutoThreshold(t *testing.T) {
	cfg := &jobspec.AutoThreshold{Q: 1e-3, Calibration: 150}
	job := autoJob(cfg)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	job.Calendars = []jobspec.Calendar{{
		Name:  "deploy",
		Start: base.Add(1190 * time.Minute),
		End:   base.Add(1210 * time.Minute),
	}}
	eng, err := New(job)
	if err != nil {
		t.Fatal(err)
	}
	pts := autoPoints(1500, map[int]float64{1200: 6}, 17)
	for _, br := range eng.Run(pts, 50) {
		if br.Time.Before(base.Add(1190*time.Minute)) || br.Time.After(base.Add(1210*time.Minute)) {
			continue
		}
		if len(br.Records) != 0 {
			t.Fatalf("a calendar window must silence the spike at %v", br.Time)
		}
	}
}

func TestAutoThresholdNormalizesConfig(t *testing.T) {
	var nilCfg *jobspec.AutoThreshold
	n := nilCfg.Normalized()
	if n.Q != 1e-4 || n.Level != 0.98 || n.Calibration != 200 || n.Min != 0 || n.Max != 100 {
		t.Fatalf("unexpected defaults: %+v", n)
	}
	bad := (&jobspec.AutoThreshold{Q: 5, Level: -1, Calibration: -3, Min: 90, Max: 10}).Normalized()
	if bad.Q != 1e-4 || bad.Level != 0.98 || bad.Calibration != 200 || bad.Min != 0 || bad.Max != 100 {
		t.Fatalf("invalid values must fall back to defaults: %+v", bad)
	}
	kept := (&jobspec.AutoThreshold{Q: 1e-6, Level: 0.9, Calibration: 42, Min: 10, Max: 20}).Normalized()
	if kept.Q != 1e-6 || kept.Level != 0.9 || kept.Calibration != 42 || kept.Min != 10 || kept.Max != 20 {
		t.Fatalf("valid values must be kept: %+v", kept)
	}
}
