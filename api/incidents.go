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

// maxChanges caps the change log. Changes are cheap to keep and only useful for
// the recent past — an incident is not explained by last month's deploy.
const maxChanges = 1000

// RecordChange appends a deliberate event (deploy, config push, traffic shift).
// This is the hook a CI pipeline calls; it is what lets an incident say "the
// deploy came first" instead of leaving a human to check.
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

// correlateAll builds the symptom + change set from everything the server holds
// and correlates it. It is the shared core of the incident endpoints and the
// ingest-triggered reconcile.
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

// handleIncidents correlates everything the server currently holds — every
// job's results plus the recorded changes — into incidents.
//
// This is the step that turns "47 anomalies" into "3 incidents, and here is
// what probably started each one". By default it returns the tracked, stateful
// view (stable ids, open/resolved status); pass ?stateless=1 for a fresh
// one-shot correlation with no lifecycle.
func (s *Server) handleIncidents(w http.ResponseWriter, r *http.Request) {
	// The /open and /resolved sub-paths return the tracked sets directly.
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
		// Fold into the tracker so ids are stable and status is reported, then
		// return the open set (plus any that just resolved this pass).
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

// reconcile feeds a fresh correlation into the tracker and alerts on the
// lifecycle events it produces. Alerting failures never stop reconciliation.
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

// reconcileFromStore recorrelates and reconciles using the server's default
// window — called on the ingest path so incidents open/resolve as data flows,
// without waiting for someone to GET /v1/incidents.
func (s *Server) reconcileFromStore(ctx context.Context) {
	incidents, _ := s.correlateAll(correlate.Options{Window: 10 * time.Minute})
	s.reconcile(ctx, incidents)
}

// incidentAlert renders a lifecycle event as an alert. A resolution is always
// info severity; an open/escalation carries the incident's peak score.
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

// handleCorrelate correlates a caller-supplied set — for tools that keep their
// own anomaly store and only want the grouping and ranking.
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

// ── query helpers ────────────────────────────────────────────────────────────

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
