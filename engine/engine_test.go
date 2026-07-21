package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func demoJob() jobspec.Job {
	return jobspec.Job{
		Name:       "t",
		BucketSpan: time.Minute,
		Detectors:  []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v"}},
	}
}

// One point per bucket over a steady baseline, with a spike near the end.
func series(start time.Time, n int, spikeAt int) []core.DataPoint {
	pts := make([]core.DataPoint, 0, n)
	base := []float64{99, 100, 101, 100, 98, 102}
	for i := 0; i < n; i++ {
		v := base[i%len(base)]
		if i == spikeAt {
			v = 500
		}
		pts = append(pts, core.DataPoint{Time: start.Add(time.Duration(i) * time.Minute), Value: v})
	}
	return pts
}

// Streaming (Push/Flush) must produce exactly the same records as batch Run.
func TestStreamingMatchesBatch(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pts := series(start, 60, 55)

	batchEng, _ := New(demoJob())
	batch := batchEng.Run(pts, 50)

	streamEng, _ := New(demoJob())
	streamEng.SetThreshold(50)
	var stream []core.BucketResult
	for _, p := range pts {
		stream = append(stream, streamEng.Push(p)...)
	}
	stream = append(stream, streamEng.Flush()...)

	batchRecs := flatten(batch)
	streamRecs := flatten(stream)
	if len(batchRecs) != len(streamRecs) {
		t.Fatalf("record count: batch=%d stream=%d", len(batchRecs), len(streamRecs))
	}
	if len(batchRecs) == 0 {
		t.Fatal("expected at least one anomaly record")
	}
	for i := range batchRecs {
		if batchRecs[i].Time != streamRecs[i].Time || batchRecs[i].Score != streamRecs[i].Score {
			t.Fatalf("record %d differs: batch=%+v stream=%+v", i, batchRecs[i], streamRecs[i])
		}
	}
}

// A snapshot taken mid-stream and restored into a fresh engine must continue
// scoring identically to the original.
func TestSnapshotRestoreContinuity(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	warm := series(start, 40, -1) // no spike, just warm up

	orig, _ := New(demoJob())
	orig.SetThreshold(50)
	for _, p := range warm {
		orig.Push(p)
	}
	snap := orig.Snapshot()

	restored, _ := New(demoJob())
	restored.Restore(snap)

	// Feed the same spike to both; results must match.
	spike := core.DataPoint{Time: start.Add(40 * time.Minute), Value: 500}
	next := core.DataPoint{Time: start.Add(41 * time.Minute), Value: 100}

	oa := append(orig.Push(spike), orig.Push(next)...)
	ra := append(restored.Push(spike), restored.Push(next)...)

	if len(flatten(oa)) != len(flatten(ra)) {
		t.Fatalf("post-restore record count differs: orig=%d restored=%d", len(flatten(oa)), len(flatten(ra)))
	}
	ro, rr := flatten(oa), flatten(ra)
	for i := range ro {
		if ro[i].Score != rr[i].Score {
			t.Fatalf("post-restore score differs at %d: %.2f vs %.2f", i, ro[i].Score, rr[i].Score)
		}
	}
}

func flatten(brs []core.BucketResult) []core.Record {
	var out []core.Record
	for _, br := range brs {
		out = append(out, br.Records...)
	}
	return out
}
