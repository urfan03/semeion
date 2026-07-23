package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/urfan03/semeion/slo"
)

const maxSLOSamples = 200_000

type sloSeries struct {
	mu      sync.Mutex
	Target  slo.Target
	samples []slo.Sample
}

func (s *sloSeries) append(samples []slo.Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples = append(s.samples, samples...)
	sort.Slice(s.samples, func(i, j int) bool { return s.samples[i].Time.Before(s.samples[j].Time) })

	if s.Target.Window > 0 && len(s.samples) > 0 {
		cutoff := s.samples[len(s.samples)-1].Time.Add(-2 * s.Target.Window)
		i := 0
		for i < len(s.samples) && s.samples[i].Time.Before(cutoff) {
			i++
		}
		s.samples = s.samples[i:]
	}
	if len(s.samples) > maxSLOSamples {
		s.samples = s.samples[len(s.samples)-maxSLOSamples:]
	}
}

func (s *sloSeries) evaluate(now time.Time) slo.Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.IsZero() {
		now = latestSample(s.samples)
		if now.IsZero() {
			now = time.Now().UTC()
		}
	}
	return slo.Evaluate(s.Target, s.samples, now)
}

func (s *Server) handleSLO(w http.ResponseWriter, r *http.Request) {
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/slo"), "/")

	if name == "" {
		switch r.Method {
		case http.MethodGet:
			s.listSLOs(w)
		case http.MethodPost:
			s.evalAdhoc(w, r)
		default:
			httpError(w, http.StatusMethodNotAllowed, "GET or POST")
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		series := s.sloSeries(name, false)
		if series == nil {
			httpError(w, http.StatusNotFound, "no SLO "+name)
			return
		}
		writeJSON(w, series.evaluate(time.Time{}))
	case http.MethodPost:
		s.appendSLO(w, r, name)
	default:
		httpError(w, http.StatusMethodNotAllowed, "GET or POST")
	}
}

type sloRequest struct {
	Objective float64      `json:"objective"`
	Window    string       `json:"window"`
	Samples   []slo.Sample `json:"samples"`
	Now       *time.Time   `json:"now"`
}

func (s *Server) evalAdhoc(w http.ResponseWriter, r *http.Request) {
	req, err := readSLO(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	win, _ := optDuration(req.Window)
	now := clockFor(req)
	writeJSON(w, slo.Evaluate(slo.Target{Objective: req.Objective, Window: win}, req.Samples, now))
}

func (s *Server) appendSLO(w http.ResponseWriter, r *http.Request, name string) {
	req, err := readSLO(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	win, err := optDuration(req.Window)
	if err != nil {
		httpError(w, http.StatusBadRequest, "window: "+err.Error())
		return
	}
	series := s.sloSeries(name, true)
	series.mu.Lock()
	if req.Objective > 0 {
		series.Target.Objective = req.Objective
	}
	if win > 0 {
		series.Target.Window = win
	}
	series.mu.Unlock()
	series.append(req.Samples)
	writeJSON(w, series.evaluate(clockOrZero(req)))
}

func (s *Server) sloSeries(name string, create bool) *sloSeries {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.slos == nil {
		s.slos = map[string]*sloSeries{}
	}
	series := s.slos[name]
	if series == nil && create {
		series = &sloSeries{}
		s.slos[name] = series
	}
	return series
}

func (s *Server) listSLOs(w http.ResponseWriter) {
	s.mu.RLock()
	names := make([]string, 0, len(s.slos))
	for k := range s.slos {
		names = append(names, k)
	}
	s.mu.RUnlock()
	sort.Strings(names)

	type row struct {
		Name     string  `json:"name"`
		SLI      float64 `json:"sli"`
		Severity string  `json:"severity"`
		Consumed float64 `json:"budget_consumed"`
		BurnRate float64 `json:"burn_rate"`
	}
	out := make([]row, 0, len(names))
	for _, n := range names {
		series := s.sloSeries(n, false)
		if series == nil {
			continue
		}
		rep := series.evaluate(time.Time{})
		out = append(out, row{n, rep.SLI, rep.Severity, rep.BudgetConsumed, rep.BurnRate})
	}
	writeJSON(w, map[string]any{"slos": out})
}

func readSLO(r *http.Request) (sloRequest, error) {
	body, err := readLimited(r)
	if err != nil {
		return sloRequest{}, err
	}
	var req sloRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return sloRequest{}, err
	}
	return req, nil
}

func clockFor(req sloRequest) time.Time {
	if req.Now != nil {
		return *req.Now
	}
	if n := latestSample(req.Samples); !n.IsZero() {
		return n
	}
	return time.Now().UTC()
}

func clockOrZero(req sloRequest) time.Time {
	if req.Now != nil {
		return *req.Now
	}
	return time.Time{}
}

func latestSample(samples []slo.Sample) time.Time {
	var latest time.Time
	for _, x := range samples {
		if x.Time.After(latest) {
			latest = x.Time
		}
	}
	return latest
}
