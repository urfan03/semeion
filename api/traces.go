package api

import (
	"net/http"

	"github.com/urfan03/semeion/otlp"
)

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

func (s *Server) handleTopology(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"nodes": s.graph.Nodes(),
		"edges": s.graph.Edges(),
	})
}
