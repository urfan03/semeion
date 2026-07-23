package alert

import (
	"context"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
)

// digestCapSink records delivered alerts for assertions.
type digestCapSink struct{ got []Alert }

func (c *digestCapSink) Name() string { return "capture" }
func (c *digestCapSink) Send(_ context.Context, a Alert) error {
	c.got = append(c.got, a)
	return nil
}

// #5: a series that keeps alerting is flagged as flapping and suppressed after
// the threshold, instead of paging on every cycle.
func TestNotifierFlappingSuppression(t *testing.T) {
	cap := &digestCapSink{}
	n := NewNotifier(cap)
	n.Dedup = 10 * time.Minute
	n.FlapThreshold = 3
	n.FlapWindow = 2 * time.Hour

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Alert every 15 minutes (clears the 10m dedup) for the same series.
	delivered := 0
	for i := 0; i < 8; i++ {
		rec := core.Record{Time: t0.Add(time.Duration(i) * 15 * time.Minute),
			Detector: "mean(v)", Series: "host=a", Score: 90}
		sent, err := n.Notify(context.Background(), "job", []core.BucketResult{{Records: []core.Record{rec}}})
		if err != nil {
			t.Fatal(err)
		}
		delivered += sent
	}
	// Threshold 3 → after 3 admitted alerts the series is flapping; the rest are
	// suppressed. So far fewer than 8 pages.
	if delivered > 3 {
		t.Fatalf("flapping should cap pages at the threshold (~3), got %d", delivered)
	}
	if n.Flapped == 0 {
		t.Fatal("flapping counter should have incremented")
	}
}

// #5: the digest folds a batch of alerts into a single summary carrying the max
// score and a per-detector breakdown.
func TestDigestSummary(t *testing.T) {
	d := NewDigest()
	if _, _, ok := d.Flush(); ok {
		t.Fatal("empty digest must not produce a summary")
	}
	t0 := time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC)
	d.Add(Alert{Job: "j", Time: t0, Detector: "mean(cpu)", Score: 55})
	d.Add(Alert{Job: "j", Time: t0.Add(20 * time.Minute), Detector: "mean(cpu)", Score: 61})
	d.Add(Alert{Job: "j", Time: t0.Add(40 * time.Minute), Detector: "count", Score: 58})

	sum, count, ok := d.Flush()
	if !ok || count != 3 {
		t.Fatalf("expected a 3-alert summary, got count=%d ok=%v", count, ok)
	}
	if sum.Kind != "digest" || sum.Score != 61 {
		t.Fatalf("summary should carry kind=digest and max score 61: %+v", sum)
	}
	if sum.Description() == "" || sum.Note == "" {
		t.Fatalf("summary should carry a text breakdown: %q", sum.Note)
	}
	// Flushing again is empty (the batch was cleared).
	if _, _, ok := d.Flush(); ok {
		t.Fatal("digest should be empty after flush")
	}
}
