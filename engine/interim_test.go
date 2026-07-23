package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func TestInterimProvisionalScore(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	job := jobspec.Job{Name: "i", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", Side: jobspec.SideHigh}}}
	eng, err := New(job)
	if err != nil {
		t.Fatal(err)
	}

	for b := 0; b < 40; b++ {
		bt := t0.Add(time.Duration(b) * time.Minute)
		eng.Push(core.DataPoint{Time: bt, Value: 100, Values: map[string]float64{"v": 100}})
	}

	openBT := t0.Add(41 * time.Minute)
	eng.Push(core.DataPoint{Time: openBT, Value: 900, Values: map[string]float64{"v": 900}})

	interim := eng.Interim()
	var got *core.Record
	for i := range interim {
		if !interim[i].Time.Equal(openBT) {
			continue
		}
		for j := range interim[i].Records {
			got = &interim[i].Records[j]
		}
	}
	if got == nil {
		t.Fatal("expected an interim record for the open anomalous bucket")
	}
	if !got.Interim {
		t.Fatal("open-bucket record must be marked is_interim")
	}
	if got.Actual != 900 {
		t.Fatalf("interim actual should be the partial value 900, got %v", got.Actual)
	}

	again := eng.Interim()
	found := false
	for _, br := range again {
		if br.Time.Equal(openBT) && len(br.Records) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("Interim() must be repeatable and non-mutating")
	}

	final := eng.Flush()
	for _, br := range final {
		if br.Time.Equal(openBT) {
			for _, r := range br.Records {
				if r.Interim {
					t.Fatal("closed-bucket record must not be marked interim")
				}
			}
		}
	}
}
