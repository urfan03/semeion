package api

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/urfan03/semeion/alert"
	"github.com/urfan03/semeion/autopilot"
	"github.com/urfan03/semeion/catalog"
	"github.com/urfan03/semeion/cluster"
	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/correlate"
	"github.com/urfan03/semeion/engine"
	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/model"
	"github.com/urfan03/semeion/outlier"
	"github.com/urfan03/semeion/stats"
	"github.com/urfan03/semeion/store"
	"github.com/urfan03/semeion/topology"
)

//go:embed explorer.html
var explorerHTML []byte

type Server struct {
	mu       sync.RWMutex
	results  map[string][]core.BucketResult
	live     map[string]*liveJob
	provider model.Provider
	notifier *alert.Notifier

	outlierDetector outlier.Detector

	changes []correlate.Change

	graph *topology.Graph

	tracker *correlate.Tracker

	slos map[string]*sloSeries

	onAlertError func(error)

	alertsSent atomic.Int64

	authToken string
	limiter   *rateLimiter

	history   *store.ResultLog
	forecasts forecastStore
	filters   filterStore

	self          string
	ring          *cluster.Ring
	clusterClient *http.Client
}

func (s *Server) WithHistory(dir string) *Server {
	if dir != "" {
		s.history = store.NewResultLog(dir)
	}
	return s
}

func (s *Server) WithAuthToken(token string) *Server {
	s.authToken = token
	return s
}

func (s *Server) WithRateLimit(rps float64) *Server {
	if rps > 0 {
		s.limiter = newRateLimiter(rps, rps*2)
	}
	return s
}

func NewServer() *Server {
	return &Server{
		results:  make(map[string][]core.BucketResult),
		live:     make(map[string]*liveJob),
		provider: model.NewGoProvider(),
		graph:    topology.New(),
		tracker:  correlate.NewTracker(),
	}
}

func (s *Server) OnAlertError(f func(error)) *Server {
	s.onAlertError = f
	return s
}

func (s *Server) WithProvider(p model.Provider) *Server {
	if p != nil {
		s.provider = p
	}
	return s
}

func (s *Server) WithOutlierDetector(d outlier.Detector) *Server {
	s.outlierDetector = d
	return s
}

func (s *Server) Store(job string, results []core.BucketResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.results[job]; !ok {
		for len(s.results) >= maxResultJobs {
			for k := range s.results {
				delete(s.results, k)
				break
			}
		}
	}
	s.results[job] = results
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/analyze", s.handleAnalyze)
	mux.HandleFunc("/v1/autopilot", s.handleAutopilot)
	mux.HandleFunc("/v1/forecast", s.handleForecast)
	mux.HandleFunc("/v1/forecasts", s.handleForecasts)
	mux.HandleFunc("/v1/forecasts/", s.handleForecasts)
	mux.HandleFunc("/v1/changepoints", s.handleChangePoints)
	mux.HandleFunc("/v1/leadlag", s.handleLeadLag)
	mux.HandleFunc("/v1/outliers", s.handleOutliers)
	mux.HandleFunc("/v1/incidents", s.handleIncidents)
	mux.HandleFunc("/v1/incidents/", s.handleIncidents)
	mux.HandleFunc("/v1/correlate", s.handleCorrelate)
	mux.HandleFunc("/v1/changes", s.handleChanges)
	mux.HandleFunc("/v1/explain/", s.handleExplain)
	mux.HandleFunc("/v1/slo", s.handleSLO)
	mux.HandleFunc("/v1/slo/", s.handleSLO)
	mux.HandleFunc("/v1/jobs", s.handleLiveJobs)
	mux.HandleFunc("/v1/jobs/", s.handleLiveJobs)
	mux.HandleFunc("/v1/results/", s.handleResults)
	mux.HandleFunc("/v1/influencers/", s.handleInfluencers)
	mux.HandleFunc("/v1/history/", s.handleHistory)
	mux.HandleFunc("/v1/catalog", s.handleCatalog)
	mux.HandleFunc("/v1/catalog/", s.handleCatalog)
	mux.HandleFunc("/v1/filters", s.handleFilters)
	mux.HandleFunc("/v1/filters/", s.handleFilters)
	mux.HandleFunc("/v1/grafana/", s.handleGrafana)

	mux.HandleFunc("/grafana/search", s.handleGrafanaSearch)
	mux.HandleFunc("/grafana/query", s.handleGrafanaQuery)
	mux.HandleFunc("/grafana/annotations", s.handleGrafanaAnnotations)
	mux.HandleFunc("/grafana/", s.handleGrafanaRoot)

	mux.HandleFunc("/v1/otlp/v1/metrics", s.handleOTLPMetrics)
	mux.HandleFunc("/v1/otlp/v1/logs", s.handleOTLPLogs)
	mux.HandleFunc("/v1/otlp/v1/traces", s.handleOTLPTraces)
	mux.HandleFunc("/v1/cloudflare/logs", s.handleCloudflareLogs)
	mux.HandleFunc("/v1/prometheus/write", s.handlePromRemoteWrite)
	mux.HandleFunc("/v1/topology", s.handleTopology)
	mux.HandleFunc("/v1/cluster", s.handleCluster)
	mux.HandleFunc("/openapi.json", s.handleOpenAPI)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("/", s.handleUI)

	var h http.Handler = mux
	h = s.withCluster(h)
	h = s.withRateLimit(h)
	h = s.withAuth(h)
	h = withBodyLimit(h)
	h = withRecover(h)
	return h
}

func withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				httpError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func withBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authToken == "" || r.URL.Path == "/healthz" || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.authToken)) != 1 {
			httpError(w, http.StatusUnauthorized, "missing or invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.limiter == nil || r.URL.Path == "/healthz" || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		if !s.limiter.allow() {
			w.Header().Set("Retry-After", "1")
			httpError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

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
	s.resolveFilters(&job)
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

		Threshold *float64 `json:"threshold,omitempty"`
		Side      string   `json:"side,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	if req.Horizon <= 0 {
		req.Horizon = 12
	}
	if req.Horizon > maxForecastHorizon {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("horizon exceeds max %d", maxForecastHorizon))
		return
	}
	if len(req.Series) > maxSeriesLen {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("series exceeds max length %d", maxSeriesLen))
		return
	}
	bands := s.provider.ForecastBands(req.Series, req.Horizon)
	resp := map[string]any{
		"periods":  s.provider.DetectSeasonality(req.Series),
		"forecast": s.provider.Forecast(req.Series, req.Horizon),
		"bands":    bands,
	}
	if req.Threshold != nil {
		resp["breach"] = model.ForecastBreach(bands, *req.Threshold, req.Side != "low")
	}
	writeJSON(w, resp)
}

func (s *Server) handleChangePoints(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		Series []float64 `json:"series"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	if len(req.Series) > maxSeriesLen {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("series exceeds max length %d", maxSeriesLen))
		return
	}
	writeJSON(w, map[string]any{
		"change_points": s.provider.ChangePoints(req.Series),
		"regimes":       model.Regimes(req.Series),
		"shift":         model.RegimeShift(req.Series),
	})
}

func (s *Server) handleLeadLag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		Target     []float64            `json:"target"`
		Candidates map[string][]float64 `json:"candidates"`
		A          []float64            `json:"a"`
		B          []float64            `json:"b"`
		MaxLag     int                  `json:"max_lag"`
		Order      int                  `json:"order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	if req.MaxLag <= 0 {
		req.MaxLag = 10
	}
	if req.Order <= 0 {
		req.Order = 3
	}
	if req.MaxLag > maxLagCap {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("max_lag exceeds max %d", maxLagCap))
		return
	}
	if req.Order > maxOrderCap {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("order exceeds max %d", maxOrderCap))
		return
	}
	if len(req.Target) > maxSeriesLen || len(req.A) > maxSeriesLen || len(req.B) > maxSeriesLen {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("series exceeds max length %d", maxSeriesLen))
		return
	}
	for _, c := range req.Candidates {
		if len(c) > maxSeriesLen {
			httpError(w, http.StatusBadRequest, fmt.Sprintf("candidate series exceeds max length %d", maxSeriesLen))
			return
		}
	}
	if len(req.Candidates) > 0 {
		writeJSON(w, map[string]any{
			"ranking": correlate.OrderByCausality(req.Target, req.Candidates, req.MaxLag, req.Order),
		})
		return
	}
	lag, corr := stats.LeadLag(req.A, req.B, req.MaxLag)
	improve, fStat := stats.Granger(req.A, req.B, req.Order)
	leads := "none"
	if lag > 0 {
		leads = "a"
	} else if lag < 0 {
		leads = "b"
	}
	writeJSON(w, map[string]any{
		"lag": lag, "corr": corr, "leads": leads,
		"granger_improvement": improve, "granger_f": fStat,
	})
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	group := r.URL.Query().Get("group")
	s.mu.RLock()
	seen := make(map[string]bool, len(s.results)+len(s.live))
	inGroup := func(name string) bool {
		if group == "" {
			return true
		}
		if lj, ok := s.live[name]; ok {
			return lj.Spec.InGroup(group)
		}
		return false
	}
	for k := range s.results {
		if inGroup(k) {
			seen[k] = true
		}
	}
	live := make([]string, 0, len(s.live))
	for k := range s.live {
		if !inGroup(k) {
			continue
		}
		seen[k] = true
		live = append(live, k)
	}
	s.mu.RUnlock()

	names := make([]string, 0, len(seen))
	for k := range seen {
		names = append(names, k)
	}
	sort.Strings(names)
	sort.Strings(live)
	writeJSON(w, map[string]any{"jobs": names, "live": live})
}

const maxBodyBytes = 32 << 20

const (
	maxForecastHorizon = 10000
	maxResultJobs      = 2000
	maxLiveJobsCount   = 2000
	maxSLOSeries       = 2000
	maxForecasts       = 2000
	maxSeriesLen       = 100000
	maxLagCap          = 1000
	maxOrderCap        = 100
)

func readLimited(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if len(body) > maxBodyBytes {
		return nil, fmt.Errorf("body larger than %d bytes", maxBodyBytes)
	}
	return body, nil
}

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

func (s *Server) handleInfluencers(w http.ResponseWriter, r *http.Request) {
	job := strings.TrimPrefix(r.URL.Path, "/v1/influencers/")
	s.mu.RLock()
	res, ok := s.results[job]
	s.mu.RUnlock()
	if !ok {
		httpError(w, http.StatusNotFound, "no results for job "+job)
		return
	}
	writeJSON(w, map[string]any{
		"job":         job,
		"influencers": correlate.RankInfluencers(res, r.URL.Query().Get("field")),
	})
}

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/v1/catalog")
	name = strings.Trim(name, "/")
	if name == "" {
		list := catalog.List()
		out := make([]map[string]string, 0, len(list))
		for _, t := range list {
			out = append(out, map[string]string{"name": t.Name, "description": t.Description})
		}
		writeJSON(w, map[string]any{"templates": out})
		return
	}
	span := time.Minute
	if v := r.URL.Query().Get("span"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			span = d
		}
	}
	job, ok := catalog.Get(name, span)
	if !ok {
		httpError(w, http.StatusNotFound, "no catalog template "+name)
		return
	}
	writeJSON(w, job)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if s.history == nil {
		httpError(w, http.StatusNotImplemented, "durable history is not enabled (serve --history DIR)")
		return
	}
	job := strings.TrimPrefix(r.URL.Path, "/v1/history/")
	q := r.URL.Query()
	var from, to time.Time
	if v := q.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = t
		}
	}
	recs, err := s.history.Query(job, from, to)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "could not read history")
		return
	}
	writeJSON(w, map[string]any{"job": job, "records": recs})
}

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
		Time     int64   `json:"time"`
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

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *Server) ListenAndServe(addr string) error {
	if s.authToken == "" {
		fmt.Fprintf(os.Stderr, "semeion: WARNING serving %s without an auth token; anyone who can reach this address has full access (set --auth-token)\n", addr)
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return srv.ListenAndServe()
}
