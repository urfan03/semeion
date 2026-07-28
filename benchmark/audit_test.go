package benchmark

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/urfan03/semeion/guard"
	"github.com/urfan03/semeion/shape"
)

func atScore(thr float64) ThresholdFunc {
	return func(CorpusSeries, []float64) float64 { return thr }
}

func auditFixture() (CorpusSeries, []float64) {
	n := 900
	scores := make([]float64, n)
	values := make([]float64, n)
	for i := range values {
		values[i] = 100
		scores[i] = 0.1
	}
	for i := 400; i < 440; i++ {
		values[i] = 160
		scores[i] = 9
	}
	for i := 700; i < 706; i++ {
		values[i] = 150
		scores[i] = 8
	}
	s, _ := syntheticSeries("grp/a.csv", n, [][2]int{{400, 439}}, scores)
	for i := range s.Points {
		s.Points[i].Value = values[i]
	}
	return s, scores
}

func TestAuditSeparatesLabelledFromUnlabelled(t *testing.T) {
	s, scores := auditFixture()
	fn := func(CorpusSeries) []float64 { return scores }
	entries := Audit([]CorpusSeries{s}, fn, AuditOptions{
		Threshold: atScore(5),
		Policy:    guard.Sensitive(),
		Gap:       5,
		Context:   100,
	})
	if len(entries) != 2 {
		t.Fatalf("expected two candidate regions, got %d: %+v", len(entries), entries)
	}

	var labelled, unlabelled *AuditEntry
	for i := range entries {
		if entries[i].Labelled {
			labelled = &entries[i]
		} else {
			unlabelled = &entries[i]
		}
	}
	if labelled == nil || unlabelled == nil {
		t.Fatalf("expected one of each: %+v", entries)
	}
	if labelled.Start != 400 || labelled.End != 439 {
		t.Fatalf("labelled region wrong: %+v", labelled)
	}
	if unlabelled.Start != 700 || unlabelled.End != 705 {
		t.Fatalf("unlabelled region wrong: %+v", unlabelled)
	}
	if unlabelled.Nearest != 700-439 {
		t.Fatalf("the gap to the nearest labelled window is wrong: %d", unlabelled.Nearest)
	}
	if labelled.Nearest != 0 {
		t.Fatalf("a labelled region has zero gap, got %d", labelled.Nearest)
	}
	if unlabelled.Shape != shape.Spike {
		t.Fatalf("a short elevation should classify as a spike, got %v", unlabelled.Shape)
	}
	if len(unlabelled.Context) == 0 || unlabelled.Offset != 600 {
		t.Fatalf("context window wrong: offset=%d len=%d", unlabelled.Offset, len(unlabelled.Context))
	}
	if unlabelled.During <= unlabelled.Before {
		t.Fatalf("the during level must exceed the baseline: %+v", unlabelled)
	}
}

func TestAuditFalseOnlyAndLimit(t *testing.T) {
	s, scores := auditFixture()
	fn := func(CorpusSeries) []float64 { return scores }
	only := Audit([]CorpusSeries{s}, fn, AuditOptions{
		Threshold: atScore(5),
		Gap:       5,
		FalseOnly: true,
	})
	if len(only) != 1 || only[0].Labelled {
		t.Fatalf("FalseOnly must keep just the unlabelled region: %+v", only)
	}

	capped := Audit([]CorpusSeries{s}, fn, AuditOptions{
		Threshold: atScore(5),
		Gap:       5,
		Limit:     1,
	})
	if len(capped) != 1 {
		t.Fatalf("the limit must be respected, got %d", len(capped))
	}
	if capped[0].Score < only[0].Score {
		t.Fatal("entries must be sorted by score so the limit keeps the strongest")
	}
}

func TestAuditSkipsUnusableSeries(t *testing.T) {
	clean, scores := syntheticSeries("clean", 50, nil, make([]float64, 50))
	fn := func(CorpusSeries) []float64 { return scores }
	if got := Audit([]CorpusSeries{clean}, fn, AuditOptions{Threshold: atScore(5)}); len(got) != 0 {
		t.Fatalf("an unlabelled series must be skipped, got %d entries", len(got))
	}

	labelled, _ := syntheticSeries("bad", 50, [][2]int{{10, 12}}, nil)
	short := Audit([]CorpusSeries{labelled}, func(CorpusSeries) []float64 { return []float64{1} },
		AuditOptions{Threshold: atScore(5)})
	if len(short) != 0 {
		t.Fatalf("a detector returning the wrong length must be skipped, got %d", len(short))
	}
}

func TestWriteAuditRoundTrips(t *testing.T) {
	s, scores := auditFixture()
	entries := Audit([]CorpusSeries{s}, func(CorpusSeries) []float64 { return scores },
		AuditOptions{Threshold: atScore(5), Gap: 5})

	var buf bytes.Buffer
	if err := WriteAudit(&buf, entries); err != nil {
		t.Fatal(err)
	}
	var back []AuditEntry
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if len(back) != len(entries) || back[0].Key != entries[0].Key || back[0].Start != entries[0].Start {
		t.Fatalf("audit entries did not round-trip: %+v", back)
	}
}
