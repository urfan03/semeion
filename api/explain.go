package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/urfan03/semeion/correlate"
	"github.com/urfan03/semeion/explain"
)

// handleExplain returns the deterministic brief for one incident, plus the
// grounded prompt a caller can hand to an LLM for prose. The incident is looked
// up first among the tracked (stateful) incidents by id, then — if not tracked
// — recomputed from the current store.
//
//	GET /v1/explain/{id}
func (s *Server) handleExplain(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/explain/")
	if id == "" {
		httpError(w, http.StatusBadRequest, "usage: GET /v1/explain/{incident-id}")
		return
	}

	inc, ok := s.findIncident(id)
	if !ok {
		httpError(w, http.StatusNotFound, "no incident "+id)
		return
	}
	brief := explain.Explain(inc)
	writeJSON(w, map[string]any{
		"brief":  brief,
		"prompt": explain.Prompt(brief), // hand to a copilot/LLM to narrate; semeion ships no model
	})
}

// findIncident resolves an id to an incident: a tracked one keeps its lifecycle,
// otherwise the current store is correlated and searched.
func (s *Server) findIncident(id string) (correlate.Incident, bool) {
	for _, tr := range s.tracker.Open() {
		if tr.ID == id {
			return tr.Incident, true
		}
	}
	for _, tr := range s.tracker.Resolved() {
		if tr.ID == id {
			return tr.Incident, true
		}
	}
	incidents, _ := s.correlateAll(correlate.Options{Window: 10 * time.Minute})
	for _, inc := range incidents {
		if inc.ID == id {
			return inc, true
		}
	}
	return correlate.Incident{}, false
}
