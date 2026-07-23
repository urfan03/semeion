package jobspec

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type fileJob struct {
	Name        string     `json:"name"`
	BucketSpan  string     `json:"bucket_span"`
	Detectors   []Detector `json:"detectors"`
	Influencers []string   `json:"influencers,omitempty"`
	Calendars   []Calendar `json:"calendars,omitempty"`
}

func LoadFile(path string) (Job, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Job{}, err
	}
	return parseJob(raw, path)
}

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
