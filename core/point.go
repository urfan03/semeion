package core

import "time"

type DataPoint struct {
	Time   time.Time
	Value  float64
	Fields map[string]string

	Values map[string]float64
}
