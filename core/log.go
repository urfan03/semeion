package core

import "time"

type LogLine struct {
	Time    time.Time
	Message string
	Fields  map[string]string
}
