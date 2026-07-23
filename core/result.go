package core

import "time"

// Influencer attributes an anomaly to a dimension value (which host / service /
// entity contributed). Elastic ML calls these "influencers".
type Influencer struct {
	Field string `json:"field"`
	Value string `json:"value"`
	// Score (0..1) is how much of the bucket's anomalous mass this value carries
	// — the share of points/metric attributable to it. 0 when not computed.
	Score float64 `json:"score,omitempty"`
}

// Direction is the sign of a deviation.
type Direction string

const (
	DirUp   Direction = "up"
	DirDown Direction = "down"
)

// Record is a single anomalous (detector, series, bucket) finding.
type Record struct {
	Time        time.Time `json:"time"`
	Detector    string    `json:"detector"`         // detector ID, e.g. "mean(latency)"
	Series      string    `json:"series,omitempty"` // by/partition key ("" = whole job)
	Actual      float64   `json:"actual"`           // observed bucket value
	Typical     float64   `json:"typical"`          // model's expected value (baseline)
	Lower       float64   `json:"lower,omitempty"`  // lower edge of the typical range (model_lower)
	Upper       float64   `json:"upper,omitempty"`  // upper edge of the typical range (model_upper)
	Probability float64   `json:"probability"`      // raw tail probability of the observation
	Score       float64   `json:"score"`            // 0..100 normalized anomaly score
	Direction   Direction `json:"direction"`
	// Log-categorization evidence (set only for categorization records).
	Kind     string `json:"kind,omitempty"`     // "metric" | "population" | "rare" | "new_category" | "rare_category" | "category_spike"
	Template string `json:"template,omitempty"` // matched log template
	Sample   string `json:"sample,omitempty"`   // the representative example raw message
	// Categorization detail (set only for categorization records).
	CategoryID int      `json:"category_id,omitempty"` // stable template/category identifier
	Examples   []string `json:"examples,omitempty"`    // several distinct example messages
	MatchCount int      `json:"match_count,omitempty"` // cumulative lines matched by this category
	// Interim marks a provisional result computed from a still-open bucket before
	// it closed (Elastic ML's is_interim). It lets a caller alert mid-bucket; the
	// definitive record — which may differ or disappear — arrives when the bucket
	// closes. Absent (false) on all final results.
	Interim bool `json:"is_interim,omitempty"`
	// Influencers rank the dimension values that contributed to this anomaly.
	Influencers []Influencer `json:"influencers,omitempty"`
}

// BucketResult aggregates every record in one time bucket.
type BucketResult struct {
	Time    time.Time `json:"time"`
	Score   float64   `json:"score"` // max record score in the bucket
	Records []Record  `json:"records,omitempty"`
}
