package api

import (
	"net/http"

	"github.com/urfan03/semeion/otlp"
)

// handleOTLPTraces accepts an OTLP/JSON trace export and folds it into the
// service dependency graph. Traces are not scored — they are the map that tells
// incident correlation which service could have caused which.
func (s *Server) handleOTLPTraces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	body, err := readLimited(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	spans, err := otlp.ParseTraces(body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.graph.Observe(spans)
	writeJSON(w, map[string]any{"spans": len(spans), "services": len(s.graph.Nodes())})
}

// handleTopology returns the current dependency graph — the nodes and edges the
// correlation engine reasons over, so it can be inspected or drawn.
func (s *Server) handleTopology(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"nodes": s.graph.Nodes(),
		"edges": s.graph.Edges(),
	})
}
