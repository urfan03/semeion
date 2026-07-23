package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/urfan03/semeion/core"
)

func (s *Server) handleGrafanaRoot(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("semeion grafana datasource ok"))
}

func (s *Server) handleGrafanaSearch(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	names := make([]string, 0, len(s.results)+len(s.live))
	seen := map[string]bool{}
	for k := range s.results {
		if !seen[k] {
			seen[k] = true
			names = append(names, k)
		}
	}
	for k := range s.live {
		if !seen[k] {
			seen[k] = true
			names = append(names, k)
		}
	}
	s.mu.RUnlock()
	sort.Strings(names)
	writeJSON(w, names)
}

type grafanaRange struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

type grafanaQueryReq struct {
	Range   grafanaRange `json:"range"`
	Targets []struct {
		Target string `json:"target"`
		Type   string `json:"type"`
	} `json:"targets"`
}

func (s *Server) handleGrafanaQuery(w http.ResponseWriter, r *http.Request) {
	var req grafanaQueryReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	inRange := func(t time.Time) bool {
		if !req.Range.From.IsZero() && t.Before(req.Range.From) {
			return false
		}
		if !req.Range.To.IsZero() && t.After(req.Range.To) {
			return false
		}
		return true
	}

	var out []any
	for _, tgt := range req.Targets {
		job := tgt.Target
		s.mu.RLock()
		res := s.results[job]
		s.mu.RUnlock()

		if tgt.Type == "table" {
			out = append(out, s.grafanaTable(job, res, inRange))
			continue
		}
		points := make([][2]float64, 0, len(res))
		for _, br := range res {
			if !inRange(br.Time) {
				continue
			}
			points = append(points, [2]float64{br.Score, float64(br.Time.UnixMilli())})
		}
		out = append(out, map[string]any{"target": job, "datapoints": points})
	}
	writeJSON(w, out)
}

func (s *Server) grafanaTable(job string, res []core.BucketResult, inRange func(time.Time) bool) map[string]any {
	rows := [][]any{}
	for _, br := range res {
		if !inRange(br.Time) {
			continue
		}
		for _, rec := range br.Records {
			rows = append(rows, []any{rec.Time.UnixMilli(), rec.Detector, rec.Series, rec.Actual, rec.Typical, rec.Score, rec.Kind})
		}
	}
	return map[string]any{
		"type": "table",
		"columns": []map[string]string{
			{"text": "Time", "type": "time"},
			{"text": "Detector", "type": "string"},
			{"text": "Series", "type": "string"},
			{"text": "Actual", "type": "number"},
			{"text": "Typical", "type": "number"},
			{"text": "Score", "type": "number"},
			{"text": "Kind", "type": "string"},
		},
		"rows": rows,
	}
}

type grafanaAnnotationReq struct {
	Range      grafanaRange `json:"range"`
	Annotation struct {
		Name  string `json:"name"`
		Query string `json:"query"`
	} `json:"annotation"`
}

func (s *Server) handleGrafanaAnnotations(w http.ResponseWriter, r *http.Request) {
	var req grafanaAnnotationReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	query := strings.TrimSpace(req.Annotation.Query)
	s.mu.RLock()
	jobs := make(map[string][]core.BucketResult, len(s.results))
	for k, v := range s.results {
		if query == "" || k == query {
			jobs[k] = v
		}
	}
	s.mu.RUnlock()

	var out []map[string]any
	for job, res := range jobs {
		for _, br := range res {
			if !req.Range.From.IsZero() && br.Time.Before(req.Range.From) {
				continue
			}
			if !req.Range.To.IsZero() && br.Time.After(req.Range.To) {
				continue
			}
			for _, rec := range br.Records {
				out = append(out, map[string]any{
					"annotation": req.Annotation,
					"time":       rec.Time.UnixMilli(),
					"title":      job + ": " + rec.Detector,
					"text":       rec.Kind,
					"tags":       []string{job, rec.Detector, rec.Series},
				})
			}
		}
	}
	writeJSON(w, out)
}
