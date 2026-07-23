package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/urfan03/semeion/alert"
	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/engine"
	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/logcat"
	"github.com/urfan03/semeion/otlp"
)

// maxLiveResults caps the per-job result ring. A live job runs forever; without
// a cap the server would grow without bound.
const maxLiveResults = 5000

// liveJob is a job whose engine stays resident: points are pushed in over time,
// buckets close as data advances, and anomalies are alerted as they appear.
//
// This is what makes `serve` a service rather than a batch analyser — the same
// engine `watch` runs, driven by pushes instead of polls.
type liveJob struct {
	mu sync.Mutex

	Name      string
	Spec      jobspec.Job
	Metric    string // OTLP metric name that feeds this job ("" = accept any)
	Threshold float64
	Logs      bool // categorization job (log lines) instead of a metric job

	eng *engine.Engine
	cat *logcat.Categorizer

	Points  int       // points/lines ingested so far
	Created time.Time // registration time
	Last    time.Time // timestamp of the newest ingested point
}

type liveJobRequest struct {
	Job       json.RawMessage `json:"job"`
	Metric    string          `json:"metric"`
	Threshold float64         `json:"threshold"`
	Logs      bool            `json:"logs"`
	// For a logs job the spec may be omitted; name + bucket_span are enough.
	Name       string `json:"name"`
	BucketSpan string `json:"bucket_span"`
}

// ── registry ─────────────────────────────────────────────────────────────────

// RegisterJob creates (or replaces) a live job. Replacing resets its baselines —
// a changed job definition must not keep the old job's learned model.
func (s *Server) RegisterJob(req liveJobRequest) (*liveJob, error) {
	lj := &liveJob{Metric: req.Metric, Threshold: req.Threshold, Logs: req.Logs, Created: time.Now().UTC()}
	if lj.Threshold <= 0 {
		lj.Threshold = 50
	}

	if req.Logs {
		span, err := parseSpan(req.BucketSpan)
		if err != nil {
			return nil, err
		}
		if req.Name == "" {
			return nil, fmt.Errorf("a logs job needs a name")
		}
		lj.Name, lj.cat = req.Name, logcat.NewCategorizer(span)
	} else {
		job, err := jobspec.Parse(req.Job)
		if err != nil {
			return nil, err
		}
		eng, err := engine.NewWithProvider(job, s.provider)
		if err != nil {
			return nil, err
		}
		lj.Name, lj.Spec, lj.eng = job.Name, job, eng
	}

	s.mu.Lock()
	if s.live == nil {
		s.live = map[string]*liveJob{}
	}
	s.live[lj.Name] = lj
	delete(s.results, lj.Name) // stale results belong to the previous definition
	s.mu.Unlock()
	return lj, nil
}

func parseSpan(s string) (time.Duration, error) {
	if s == "" {
		return time.Minute, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("bucket_span: %w", err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("bucket_span must be positive")
	}
	return d, nil
}

func (s *Server) liveJob(name string) (*liveJob, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lj, ok := s.live[name]
	return lj, ok
}

// WithNotifier routes live-job anomalies to alert sinks.
func (s *Server) WithNotifier(n *alert.Notifier) *Server {
	s.notifier = n
	return s
}

// ── ingestion ────────────────────────────────────────────────────────────────

// pushPoints feeds points into a live job and publishes whatever closed.
func (s *Server) pushPoints(ctx context.Context, lj *liveJob, points []core.DataPoint) []core.BucketResult {
	if len(points) == 0 {
		return nil
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Time.Before(points[j].Time) })

	lj.mu.Lock()
	var closed []core.BucketResult
	for _, p := range points {
		closed = append(closed, lj.eng.Push(p)...)
	}
	lj.Points += len(points)
	lj.Last = points[len(points)-1].Time
	lj.mu.Unlock()

	return s.publish(ctx, lj, closed)
}

// pushLogs is the categorization equivalent of pushPoints.
func (s *Server) pushLogs(ctx context.Context, lj *liveJob, lines []core.LogLine) []core.BucketResult {
	if len(lines) == 0 {
		return nil
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].Time.Before(lines[j].Time) })

	lj.mu.Lock()
	var closed []core.BucketResult
	for _, l := range lines {
		closed = append(closed, lj.cat.Push(l, lj.Threshold)...)
	}
	lj.Points += len(lines)
	lj.Last = lines[len(lines)-1].Time
	lj.mu.Unlock()

	return s.publish(ctx, lj, closed)
}

// publish keeps only buckets that carry an above-threshold record, stores them
// on the job's ring, and alerts.
func (s *Server) publish(ctx context.Context, lj *liveJob, closed []core.BucketResult) []core.BucketResult {
	kept := make([]core.BucketResult, 0, len(closed))
	for _, br := range closed {
		recs := make([]core.Record, 0, len(br.Records))
		for _, r := range br.Records {
			if r.Score >= lj.Threshold {
				recs = append(recs, r)
			}
		}
		if len(recs) == 0 {
			continue
		}
		br.Records = recs
		kept = append(kept, br)
	}
	if len(kept) == 0 {
		return nil
	}

	s.mu.Lock()
	res := append(s.results[lj.Name], kept...)
	if len(res) > maxLiveResults {
		res = res[len(res)-maxLiveResults:]
	}
	s.results[lj.Name] = res
	n := s.notifier
	s.mu.Unlock()

	if n != nil {
		sent, err := n.Notify(ctx, lj.Name, kept)
		s.alertsSent.Add(int64(sent))
		if err != nil && s.onAlertError != nil {
			s.onAlertError(err)
		}
	}

	// A new anomaly may open, grow or escalate an incident. Reconcile only when
	// something was actually published, so the O(symptoms) correlation never
	// runs on a quiet bucket.
	if s.tracker != nil {
		s.reconcileFromStore(ctx)
	}
	return kept
}

// ── handlers ─────────────────────────────────────────────────────────────────

// handleLiveJobs implements POST /v1/jobs (create), and the /v1/jobs/{name}
// sub-routes: GET (status), DELETE (remove), POST …/points, POST …/flush.
func (s *Server) handleLiveJobs(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/jobs"), "/")

	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			s.handleJobs(w, r)
		case http.MethodPost:
			s.handleCreateJob(w, r)
		default:
			httpError(w, http.StatusMethodNotAllowed, "GET or POST")
		}
		return
	}

	name, action, _ := strings.Cut(rest, "/")
	lj, ok := s.liveJob(name)
	if !ok {
		httpError(w, http.StatusNotFound, "no live job "+name)
		return
	}
	switch {
	case action == "" && r.Method == http.MethodGet:
		writeJSON(w, jobStatus(lj))
	case action == "" && r.Method == http.MethodDelete:
		s.mu.Lock()
		delete(s.live, name)
		s.mu.Unlock()
		writeJSON(w, map[string]string{"deleted": name})
	case action == "points" && r.Method == http.MethodPost:
		s.handlePushPoints(w, r, lj)
	case action == "flush" && r.Method == http.MethodPost:
		s.handleFlush(w, r, lj)
	case action == "interim" && r.Method == http.MethodGet:
		s.handleInterim(w, r, lj)
	case action == "categories" && r.Method == http.MethodGet:
		s.handleCategories(w, r, lj)
	default:
		httpError(w, http.StatusNotFound, "unknown route")
	}
}

// handleCategories returns the learned log-category catalogue (id, template,
// examples, match counts) for a categorization job. Metric jobs have none.
func (s *Server) handleCategories(w http.ResponseWriter, _ *http.Request, lj *liveJob) {
	if !lj.Logs {
		writeJSON(w, map[string]any{"job": lj.Name, "categories": []logcat.CategoryDefinition{}})
		return
	}
	lj.mu.Lock()
	cats := lj.cat.Categories()
	lj.mu.Unlock()
	writeJSON(w, map[string]any{"job": lj.Name, "categories": cats})
}

// handleInterim returns provisional (is_interim) results for the job's still-open
// buckets, scored against the current baseline without closing them — a
// mid-bucket peek for real-time alerting. Logs (categorization) jobs have no
// interim path and return an empty set.
func (s *Server) handleInterim(w http.ResponseWriter, _ *http.Request, lj *liveJob) {
	if lj.Logs {
		writeJSON(w, map[string]any{"job": lj.Name, "interim": []core.BucketResult{}})
		return
	}
	lj.mu.Lock()
	all := lj.eng.Interim()
	lj.mu.Unlock()
	// Keep only buckets that carry an above-threshold provisional record.
	kept := make([]core.BucketResult, 0, len(all))
	for _, br := range all {
		recs := make([]core.Record, 0, len(br.Records))
		for _, r := range br.Records {
			if r.Score >= lj.Threshold {
				recs = append(recs, r)
			}
		}
		if len(recs) == 0 {
			continue
		}
		br.Records = recs
		kept = append(kept, br)
	}
	writeJSON(w, map[string]any{"job": lj.Name, "interim": kept})
}

func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var req liveJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	lj, err := s.RegisterJob(req)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Content-Type must be set before WriteHeader, or it is ignored.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(jobStatus(lj))
}

func (s *Server) handlePushPoints(w http.ResponseWriter, r *http.Request, lj *liveJob) {
	var req struct {
		Points []core.DataPoint `json:"points"`
		Logs   []core.LogLine   `json:"logs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	var out []core.BucketResult
	if lj.Logs {
		out = s.pushLogs(r.Context(), lj, req.Logs)
	} else {
		out = s.pushPoints(r.Context(), lj, req.Points)
	}
	writeJSON(w, map[string]any{"job": lj.Name, "anomalies": out})
}

// handleFlush closes the still-open bucket — for the end of a backfill, or a
// low-traffic job where the next point may be a long way off.
func (s *Server) handleFlush(w http.ResponseWriter, r *http.Request, lj *liveJob) {
	lj.mu.Lock()
	var closed []core.BucketResult
	if lj.Logs {
		closed = lj.cat.Flush(lj.Threshold)
	} else {
		closed = lj.eng.Flush()
	}
	lj.mu.Unlock()
	writeJSON(w, map[string]any{"job": lj.Name, "anomalies": s.publish(r.Context(), lj, closed)})
}

// handleOTLPMetrics accepts an OTLP/JSON metrics export and fans each data point
// to every live job that claims its metric name.
func (s *Server) handleOTLPMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	body, err := readLimited(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	mps, err := otlp.ParseMetrics(body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	byJob := map[*liveJob][]core.DataPoint{}
	for _, mp := range mps {
		for _, lj := range s.metricJobs(mp.Metric) {
			byJob[lj] = append(byJob[lj], mp.Point)
		}
	}
	anomalies := 0
	for lj, pts := range byJob {
		anomalies += len(s.pushPoints(r.Context(), lj, pts))
	}
	writeJSON(w, map[string]any{"accepted": len(mps), "jobs": len(byJob), "anomalies": anomalies})
}

// handleOTLPLogs accepts an OTLP/JSON logs export and feeds every live logs job.
func (s *Server) handleOTLPLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	body, err := readLimited(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	lines, err := otlp.ParseLogs(body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	jobs := 0
	anomalies := 0
	for _, lj := range s.logJobs() {
		jobs++
		anomalies += len(s.pushLogs(r.Context(), lj, lines))
	}
	writeJSON(w, map[string]any{"accepted": len(lines), "jobs": jobs, "anomalies": anomalies})
}

// metricJobs returns the metric jobs a named OTLP metric feeds. A job with no
// Metric set accepts everything — handy for a single-metric collector pipeline.
func (s *Server) metricJobs(metric string) []*liveJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*liveJob
	for _, lj := range s.live {
		if !lj.Logs && (lj.Metric == "" || lj.Metric == metric) {
			out = append(out, lj)
		}
	}
	return out
}

func (s *Server) logJobs() []*liveJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*liveJob
	for _, lj := range s.live {
		if lj.Logs {
			out = append(out, lj)
		}
	}
	return out
}

func jobStatus(lj *liveJob) map[string]any {
	lj.mu.Lock()
	defer lj.mu.Unlock()
	st := map[string]any{
		"name": lj.Name, "logs": lj.Logs, "threshold": lj.Threshold,
		"points": lj.Points, "created": lj.Created,
	}
	if lj.Metric != "" {
		st["metric"] = lj.Metric
	}
	if !lj.Last.IsZero() {
		st["last_point"] = lj.Last
	}
	if !lj.Logs {
		st["bucket_span"] = lj.Spec.BucketSpan.String()
		st["detectors"] = lj.Spec.Detectors
	}
	return st
}
