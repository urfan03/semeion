// Package api turns semeion into a service: a small stdlib HTTP server that
// analyses posted data, keeps the latest results per job, serves them to
// Grafana, and ships an embedded Anomaly Explorer UI. No framework, no external
// dependencies — still one static binary.
package api

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/urfan03/semeion/autopilot"
	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/engine"
	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/model"
)

//go:embed explorer.html
var explorerHTML []byte

// Server holds the latest results per job (in memory) and serves the API + UI.
type Server struct {
	mu       sync.RWMutex
	results  map[string][]core.BucketResult
	provider model.Provider
}

// NewServer builds a server with the default (pure-Go) model provider.
func NewServer() *Server {
	return &Server{results: make(map[string][]core.BucketResult), provider: model.NewGoProvider()}
}

// WithProvider swaps the heavy-model provider (e.g. the Python plane).
func (s *Server) WithProvider(p model.Provider) *Server {
	if p != nil {
		s.provider = p
	}
	return s
}

// Store publishes results under a job name (used to seed the demo).
func (s *Server) Store(job string, results []core.BucketResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results[job] = results
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/analyze", s.handleAnalyze)
	mux.HandleFunc("/v1/autopilot", s.handleAutopilot)
	mux.HandleFunc("/v1/forecast", s.handleForecast)
	mux.HandleFunc("/v1/jobs", s.handleJobs)
	mux.HandleFunc("/v1/results/", s.handleResults)
	mux.HandleFunc("/v1/grafana/", s.handleGrafana)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("/", s.handleUI)
	return mux
}

// ── handlers ─────────────────────────────────────────────────────────────────

type analyzeRequest struct {
	Job         json.RawMessage  `json:"job"`
	Points      []core.DataPoint `json:"points"`
	Threshold   float64          `json:"threshold"`
	Renormalize bool             `json:"renormalize"`
}

type analyzeResponse struct {
	Job      string              `json:"job"`
	Buckets  int                 `json:"buckets"`
	Records  int                 `json:"records"`
	Results  []core.BucketResult `json:"results"`
	Warnings []string            `json:"warnings,omitempty"`
}

func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req analyzeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	job, err := jobspec.Parse(req.Job)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Threshold <= 0 {
		req.Threshold = 50
	}
	eng, err := engine.NewWithProvider(job, s.provider)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	results := eng.Run(req.Points, req.Threshold)
	if req.Renormalize {
		engine.RenormalizeResults(results)
	}
	s.Store(job.Name, results)

	n := 0
	for _, br := range results {
		n += len(br.Records)
	}
	writeJSON(w, analyzeResponse{Job: job.Name, Buckets: len(results), Records: n, Results: results})
}

func (s *Server) handleAutopilot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		Points []core.DataPoint `json:"points"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	job := autopilot.Suggest(req.Points)
	writeJSON(w, map[string]any{
		"name":        job.Name,
		"bucket_span": job.BucketSpan.String(),
		"detectors":   job.Detectors,
	})
}

func (s *Server) handleForecast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		Series  []float64 `json:"series"`
		Horizon int       `json:"horizon"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	if req.Horizon <= 0 {
		req.Horizon = 12
	}
	writeJSON(w, map[string]any{
		"periods":  s.provider.DetectSeasonality(req.Series),
		"forecast": s.provider.Forecast(req.Series, req.Horizon),
	})
}

// handleJobs lists the job names that have stored results.
func (s *Server) handleJobs(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.results))
	for k := range s.results {
		names = append(names, k)
	}
	writeJSON(w, map[string]any{"jobs": names})
}

// handleResults returns the stored results for /v1/results/{job}.
func (s *Server) handleResults(w http.ResponseWriter, r *http.Request) {
	job := strings.TrimPrefix(r.URL.Path, "/v1/results/")
	s.mu.RLock()
	res, ok := s.results[job]
	s.mu.RUnlock()
	if !ok {
		httpError(w, http.StatusNotFound, "no results for job "+job)
		return
	}
	writeJSON(w, map[string]any{"job": job, "results": res})
}

// handleGrafana returns a flat time/score series for /v1/grafana/{job} — the
// shape Grafana's Infinity (or any JSON) datasource can graph directly.
func (s *Server) handleGrafana(w http.ResponseWriter, r *http.Request) {
	job := strings.TrimPrefix(r.URL.Path, "/v1/grafana/")
	s.mu.RLock()
	res, ok := s.results[job]
	s.mu.RUnlock()
	if !ok {
		httpError(w, http.StatusNotFound, "no results for job "+job)
		return
	}
	type point struct {
		Time     int64   `json:"time"` // epoch millis (Grafana convention)
		Score    float64 `json:"score"`
		Detector string  `json:"detector,omitempty"`
		Series   string  `json:"series,omitempty"`
		Kind     string  `json:"kind,omitempty"`
	}
	out := make([]point, 0, len(res))
	for _, br := range res {
		p := point{Time: br.Time.UnixMilli(), Score: br.Score}
		if len(br.Records) > 0 {
			r0 := br.Records[0]
			p.Detector, p.Series, p.Kind = r0.Detector, r0.Series, r0.Kind
		}
		out = append(out, p)
	}
	writeJSON(w, out)
}

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/ui" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(explorerHTML)
}

// ── helpers ──────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ListenAndServe starts the API + UI on addr.
func (s *Server) ListenAndServe(addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}
