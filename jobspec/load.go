package jobspec

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// fileJob mirrors Job for on-disk config, but takes bucket_span as a human
// duration string ("5m", "1h") instead of raw nanoseconds.
type fileJob struct {
	Name        string     `json:"name"`
	BucketSpan  string     `json:"bucket_span"`
	Detectors   []Detector `json:"detectors"`
	Influencers []string   `json:"influencers,omitempty"`
	Calendars   []Calendar `json:"calendars,omitempty"`
}

// LoadFile reads and validates a job definition from a JSON file. bucket_span
// is a Go duration string (e.g. "5m", "1h", "30s").
func LoadFile(path string) (Job, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Job{}, err
	}
	return parseJob(raw, path)
}

// Parse decodes and validates a job from JSON bytes (bucket_span as a duration
// string). Used by the API to accept jobs over the wire.
func Parse(raw []byte) (Job, error) { return parseJob(raw, "request") }

func parseJob(raw []byte, src string) (Job, error) {
	var f fileJob
	if err := json.Unmarshal(raw, &f); err != nil {
		return Job{}, fmt.Errorf("parse job %s: %w", src, err)
	}
	span, err := time.ParseDuration(f.BucketSpan)
	if err != nil {
		return Job{}, fmt.Errorf("job %s: bucket_span %q: %w", src, f.BucketSpan, err)
	}
	job := Job{Name: f.Name, BucketSpan: span, Detectors: f.Detectors, Influencers: f.Influencers, Calendars: f.Calendars}
	if err := job.Validate(); err != nil {
		return Job{}, err
	}
	return job, nil
}
