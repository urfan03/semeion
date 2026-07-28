package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/urfan03/semeion/guard"
	"github.com/urfan03/semeion/pipeline"
)

const (
	maxDetectSeries = 2000
	maxDetectPoints = 50_000
)

type detectConfig struct {
	Sensitivity string `json:"sensitivity,omitempty"`
	History     int    `json:"history,omitempty"`
	Calibration int    `json:"calibration,omitempty"`
	Refresh     int    `json:"refresh,omitempty"`
	Period      int    `json:"period,omitempty"`
	Deseasonal  bool   `json:"deseasonal,omitempty"`

	BudgetAlarms int     `json:"budget_alarms,omitempty"`
	BudgetPer    int     `json:"budget_per,omitempty"`
	MinEffect    float64 `json:"min_effect,omitempty"`
	MinDuration  int     `json:"min_duration,omitempty"`
	Q            float64 `json:"q,omitempty"`
}

func (c detectConfig) options() pipeline.Options {
	return pipeline.Options{
		Sensitivity: pipeline.Sensitivity(c.Sensitivity),
		History:     c.History,
		Calibration: c.Calibration,
		Refresh:     c.Refresh,
		Period:      c.Period,
		Deseasonal:  c.Deseasonal,
		Budget:      guard.Budget{Alarms: c.BudgetAlarms, Per: c.BudgetPer},
		MinEffect:   c.MinEffect,
		MinDuration: c.MinDuration,
		Q:           c.Q,
	}
}

type detectSeries struct {
	Key    string    `json:"key"`
	Values []float64 `json:"values"`
}

type detectRequest struct {
	Config detectConfig   `json:"config"`
	Series []detectSeries `json:"series"`
}

type detectVerdict struct {
	Key      string           `json:"key"`
	Fired    bool             `json:"fired"`
	Score    float64          `json:"score"`
	P        float64          `json:"p_value"`
	Effect   float64          `json:"effect"`
	Duration int              `json:"duration"`
	Shape    string           `json:"shape"`
	Reason   string           `json:"reason"`
	Alarms   []pipeline.Alarm `json:"alarms,omitempty"`
	Skipped  string           `json:"skipped,omitempty"`
}

type detectResponse struct {
	Verdicts []detectVerdict `json:"verdicts"`
}

// handleDetect scores a batch of series with the production pipeline. Fired
// reports whether the LAST point of a series sits inside an alarm region, which
// is the question a periodic scan asks; Alarms carries every region found so a
// caller can backfill.
func (s *Server) handleDetect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	body, err := readLimited(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req detectRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("invalid body: %v", err))
		return
	}
	if len(req.Series) == 0 {
		httpError(w, http.StatusBadRequest, "series is required")
		return
	}
	if len(req.Series) > maxDetectSeries {
		httpError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("%d series exceeds the %d per request limit", len(req.Series), maxDetectSeries))
		return
	}
	if _, err := pipeline.New(req.Config.options()); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	out := detectResponse{Verdicts: make([]detectVerdict, 0, len(req.Series))}
	for _, sr := range req.Series {
		v := detectVerdict{Key: sr.Key}
		switch {
		case len(sr.Values) == 0:
			v.Skipped = "no values"
		case len(sr.Values) > maxDetectPoints:
			v.Skipped = fmt.Sprintf("%d points exceeds the %d limit", len(sr.Values), maxDetectPoints)
		default:
			d, err := pipeline.New(req.Config.options())
			if err != nil {
				v.Skipped = err.Error()
				break
			}
			alarms := d.Scan(sr.Values)
			if !d.Ready() {
				v.Skipped = "not enough history to calibrate"
				break
			}
			v.Alarms = alarms
			last := len(sr.Values) - 1
			for _, a := range alarms {
				if a.Start <= last && a.End >= last {
					v.Fired = true
					v.Score, v.P = a.Score, a.P
					v.Effect, v.Duration = a.Effect, a.Duration
					v.Shape, v.Reason = string(a.Shape), a.Reason
					break
				}
			}
			if !v.Fired && len(alarms) > 0 {
				a := alarms[len(alarms)-1]
				v.Score, v.P = a.Score, a.P
				v.Effect, v.Duration = a.Effect, a.Duration
				v.Shape, v.Reason = string(a.Shape), a.Reason
			}
		}
		out.Verdicts = append(out.Verdicts, v)
	}
	writeJSON(w, out)
}
