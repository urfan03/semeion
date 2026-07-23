package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/urfan03/semeion/alert"
	"github.com/urfan03/semeion/correlate"
)

const maxChanges = 1000

func (s *Server) RecordChange(c correlate.Change) {
	if c.Time.IsZero() {
		c.Time = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.changes = append(s.changes, c)
	if len(s.changes) > maxChanges {
		s.changes = s.changes[len(s.changes)-maxChanges:]
	}
}

func (s *Server) handleChanges(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		out := append([]correlate.Change(nil), s.changes...)
		s.mu.RUnlock()
		writeJSON(w, map[string]any{"changes": out})
	case http.MethodPost:
		var c correlate.Change
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			httpError(w, http.StatusBadRequest, "decode: "+err.Error())
			return
		}
		if c.Name == "" {
			httpError(w, http.StatusBadRequest, "a change needs a name")
			return
		}
		s.RecordChange(c)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(c)
	default:
		httpError(w, http.StatusMethodNotAllowed, "GET or POST")
	}
}

func (s *Server) correlateAll(opt correlate.Options) (incidents []correlate.Incident, symptoms int) {
	if !s.graph.Empty() {
		opt.Topology = s.graph
	}
	s.mu.RLock()
	var syms []correlate.Symptom
	for job, res := range s.results {
		syms = append(syms, correlate.FromRecords(job, res)...)
	}
	changes := append([]correlate.Change(nil), s.changes...)
	s.mu.RUnlock()

	return correlate.Correlate(syms, changes, opt), len(syms)
}

func (s *Server) handleIncidents(w http.ResponseWriter, r *http.Request) {

	switch strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/incidents"), "/") {
	case "open":
		writeJSON(w, map[string]any{"incidents": s.tracker.Open()})
		return
	case "resolved":
		writeJSON(w, map[string]any{"incidents": s.tracker.Resolved()})
		return
	}

	opt := correlate.Options{
		Window:   queryDuration(r, "window", 10*time.Minute),
		CoWindow: queryDuration(r, "co_window", 0),
		MinScore: queryFloat(r, "min_score", 0),
	}
	incidents, symptoms := s.correlateAll(opt)

	if r.URL.Query().Get("stateless") == "" {

		s.reconcile(r.Context(), incidents)
		tracked := s.tracker.Open()
		if top := queryInt(r, "top", 0); top > 0 && top < len(tracked) {
			tracked = tracked[:top]
		}
		writeJSON(w, map[string]any{"window": opt.Window.String(), "symptoms": symptoms, "incidents": tracked})
		return
	}

	if top := queryInt(r, "top", 0); top > 0 && top < len(incidents) {
		incidents = incidents[:top]
	}
	writeJSON(w, map[string]any{"window": opt.Window.String(), "symptoms": symptoms, "incidents": incidents})
}

func (s *Server) reconcile(ctx context.Context, incidents []correlate.Incident) []correlate.Event {
	events := s.tracker.Reconcile(incidents)
	s.mu.RLock()
	n := s.notifier
	s.mu.RUnlock()
	if n == nil {
		return events
	}
	for _, ev := range events {
		delivered, err := n.Deliver(ctx, incidentAlert(ev))
		if delivered {
			s.alertsSent.Add(1)
		}
		if err != nil && s.onAlertError != nil {
			s.onAlertError(err)
		}
	}
	return events
}

func (s *Server) reconcileFromStore(ctx context.Context) {
	incidents, _ := s.correlateAll(correlate.Options{Window: 10 * time.Minute})
	s.reconcile(ctx, incidents)
}

func incidentAlert(ev correlate.Event) alert.Alert {
	inc := ev.Incident
	score := inc.PeakScore
	if ev.Kind == correlate.Resolved {
		score = 0
	}
	a := alert.Alert{
		Job:      "incident",
		Time:     inc.LastActivity,
		Detector: string(ev.Kind),
		Series:   inc.ID,
		Score:    score,
		Kind:     "incident",
	}
	origin := "unknown"
	if len(inc.RootCause) > 0 {
		c := inc.RootCause[0]
		if c.Change != nil {
			origin = "change " + c.Change.Name
		} else if svc := c.Symptom.Entities["service"]; svc != "" {
			origin = svc
		} else {
			origin = c.Symptom.Job
		}
	}
	a.Template = fmt.Sprintf("incident %s: %s — likely origin: %s", strings.ToUpper(string(ev.Kind)), inc.Summary, origin)
	return a
}

func (s *Server) handleCorrelate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		Symptoms []correlate.Symptom `json:"symptoms"`
		Changes  []correlate.Change  `json:"changes"`
		Window   string              `json:"window"`
		CoWindow string              `json:"co_window"`
		MinScore float64             `json:"min_score"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	opt := correlate.Options{MinScore: req.MinScore}
	var err error
	if opt.Window, err = optDuration(req.Window); err != nil {
		httpError(w, http.StatusBadRequest, "window: "+err.Error())
		return
	}
	if opt.CoWindow, err = optDuration(req.CoWindow); err != nil {
		httpError(w, http.StatusBadRequest, "co_window: "+err.Error())
		return
	}
	incidents := correlate.Correlate(req.Symptoms, req.Changes, opt)
	writeJSON(w, map[string]any{"symptoms": len(req.Symptoms), "incidents": incidents})
}

func optDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}

func queryDuration(r *http.Request, key string, def time.Duration) time.Duration {
	if v := r.URL.Query().Get(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func queryFloat(r *http.Request, key string, def float64) float64 {
	if v := r.URL.Query().Get(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func queryInt(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}
