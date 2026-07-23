package core

import "time"

type Influencer struct {
	Field string `json:"field"`
	Value string `json:"value"`

	Score float64 `json:"score,omitempty"`
}

type Direction string

const (
	DirUp   Direction = "up"
	DirDown Direction = "down"
)

type Record struct {
	Time        time.Time `json:"time"`
	Detector    string    `json:"detector"`
	Series      string    `json:"series,omitempty"`
	Actual      float64   `json:"actual"`
	Typical     float64   `json:"typical"`
	Lower       float64   `json:"lower,omitempty"`
	Upper       float64   `json:"upper,omitempty"`
	Probability float64   `json:"probability"`
	Score       float64   `json:"score"`
	// InitialScore is the score at the time the bucket was first analysed, before
	// any later renormalization rescaled it (Elastic ML's initial_record_score).
	InitialScore float64   `json:"initial_score,omitempty"`
	Direction    Direction `json:"direction"`

	Kind     string `json:"kind,omitempty"`
	Template string `json:"template,omitempty"`
	Sample   string `json:"sample,omitempty"`

	CategoryID int      `json:"category_id,omitempty"`
	Examples   []string `json:"examples,omitempty"`
	MatchCount int      `json:"match_count,omitempty"`

	Interim bool `json:"is_interim,omitempty"`
	// MultiBucketImpact (0..5) is how much a sustained multi-bucket deviation
	// drove this anomaly beyond the single-bucket score (Elastic ML's
	// multi_bucket_impact). 0 when the anomaly is purely single-bucket.
	MultiBucketImpact float64 `json:"multi_bucket_impact,omitempty"`

	Influencers []Influencer `json:"influencers,omitempty"`
}

type BucketResult struct {
	Time         time.Time `json:"time"`
	Score        float64   `json:"score"`
	InitialScore float64   `json:"initial_score,omitempty"`
	Records      []Record  `json:"records,omitempty"`
}
