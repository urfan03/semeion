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
	Direction   Direction `json:"direction"`

	Kind     string `json:"kind,omitempty"`
	Template string `json:"template,omitempty"`
	Sample   string `json:"sample,omitempty"`

	CategoryID int      `json:"category_id,omitempty"`
	Examples   []string `json:"examples,omitempty"`
	MatchCount int      `json:"match_count,omitempty"`

	Interim bool `json:"is_interim,omitempty"`

	Influencers []Influencer `json:"influencers,omitempty"`
}

type BucketResult struct {
	Time    time.Time `json:"time"`
	Score   float64   `json:"score"`
	Records []Record  `json:"records,omitempty"`
}
