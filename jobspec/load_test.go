package jobspec

import "testing"

func TestParseJobValid(t *testing.T) {
	raw := []byte(`{
		"name": "latency",
		"bucket_span": "5m",
		"detectors": [{ "function": "mean", "field": "latency", "side": "high" }]
	}`)
	job, err := parseJob(raw, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if job.BucketSpan.Minutes() != 5 {
		t.Fatalf("bucket_span: got %v", job.BucketSpan)
	}
	if len(job.Detectors) != 1 || job.Detectors[0].ID() != "mean(latency)" {
		t.Fatalf("detector: got %+v", job.Detectors)
	}
	if job.Detectors[0].EffectiveSide() != SideHigh {
		t.Fatalf("side: got %v", job.Detectors[0].Side)
	}
}

func TestParseJobBadSpan(t *testing.T) {
	raw := []byte(`{"name":"x","bucket_span":"nope","detectors":[{"function":"count"}]}`)
	if _, err := parseJob(raw, "test"); err == nil {
		t.Fatal("expected error for bad bucket_span")
	}
}

func TestParseJobMissingField(t *testing.T) {
	raw := []byte(`{"name":"x","bucket_span":"1m","detectors":[{"function":"mean"}]}`)
	if _, err := parseJob(raw, "test"); err == nil {
		t.Fatal("expected error for mean detector without field")
	}
}
