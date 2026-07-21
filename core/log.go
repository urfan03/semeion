package core

import "time"

// LogLine is a single unstructured log record fed to the categorization
// detector. Fields carries structured dimensions (service, host, level, …) used
// for influencer attribution and splitting.
type LogLine struct {
	Time    time.Time
	Message string
	Fields  map[string]string
}
