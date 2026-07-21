// Package core holds the value types shared across the engine: the input
// DataPoint and the output Record / BucketResult. Pure data, no behaviour.
package core

import "time"

// DataPoint is a single observation fed to the engine. For metric detectors the
// detector's field value is carried in Value; Fields holds the dimensions used
// for by/partition splitting and influencer attribution.
type DataPoint struct {
	Time   time.Time
	Value  float64
	Fields map[string]string
	// Values holds named numeric metrics for MULTIVARIATE detectors (e.g.
	// {"cpu":91,"latency":420,"rps":1200}). Optional; single-metric detectors
	// use Value.
	Values map[string]float64
}
