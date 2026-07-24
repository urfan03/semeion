package api

import (
	"bytes"
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
	"github.com/urfan03/semeion/datafeed"
	"github.com/urfan03/semeion/engine"
	"github.com/urfan03/semeion/ingest"
	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/logcat"
	"github.com/urfan03/semeion/otlp"
)

const maxLiveResults = 5000

type liveJob struct {
	mu sync.Mutex

	Name      string
	Spec      jobspec.Job
	Metric    string
	Threshold float64
	Logs      bool

	eng *engine.Engine
	cat *logcat.Categorizer

	Points  int
	Created time.Time
	Last    time.Time
}

type liveJobRequest struct {
	Job       json.RawMessage `json:"job"`
	Metric    string          `json:"metric"`
	Threshold float64         `json:"threshold"`
	Logs      bool            `json:"logs"`

	Name       string `json:"name"`
	BucketSpan string `json:"bucket_span"`
}

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
	if _, exists := s.live[lj.Name]; !exists && len(s.live) >= maxLiveJobsCount {
		s.mu.Unlock()
		return nil, fmt.Errorf("live job limit reached (%d)", maxLiveJobsCount)
	}
	s.live[lj.Name] = lj
	delete(s.results, lj.Name)
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

func (s *Server) WithNotifier(n *alert.Notifier) *Server {
	s.notifier = n
	return s
}

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
	hist := s.history
	s.mu.Unlock()

	if hist != nil {
		_ = hist.Append(lj.Name, kept)
	}

	if n != nil {
		sent, err := n.Notify(ctx, lj.Name, kept)
		s.alertsSent.Add(int64(sent))
		if err != nil && s.onAlertError != nil {
			s.onAlertError(err)
		}
	}

	if s.tracker != nil {
		s.reconcileFromStore(ctx)
	}
	return kept
}

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
	case action == "stale" && r.Method == http.MethodGet:
		s.handleStale(w, r, lj)
	case action == "feedback" && r.Method == http.MethodPost:
		s.handleFeedback(w, r, lj)
	default:
		httpError(w, http.StatusNotFound, "unknown route")
	}
}

func (s *Server) handleFeedback(w http.ResponseWriter, r *http.Request, lj *liveJob) {
	if lj.Logs {
		httpError(w, http.StatusBadRequest, "feedback applies to metric jobs")
		return
	}
	var req struct {
		Detector      string `json:"detector"`
		Series        string `json:"series"`
		FalsePositive bool   `json:"false_positive"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	if req.Detector == "" {
		httpError(w, http.StatusBadRequest, "detector is required")
		return
	}
	lj.mu.Lock()
	if req.FalsePositive {
		lj.eng.MarkFalsePositive(req.Detector, req.Series)
	} else {
		lj.eng.ClearFeedback(req.Detector, req.Series)
	}
	lj.mu.Unlock()
	writeJSON(w, map[string]any{"job": lj.Name, "detector": req.Detector, "series": req.Series, "false_positive": req.FalsePositive})
}

func (s *Server) handleStale(w http.ResponseWriter, r *http.Request, lj *liveJob) {
	if lj.Logs {
		writeJSON(w, map[string]any{"job": lj.Name, "stale": []engine.StaleSeries{}})
		return
	}
	maxAge := lj.Spec.BucketSpan * 5
	if v := r.URL.Query().Get("max_age"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			maxAge = d
		}
	}
	lj.mu.Lock()
	stale := lj.eng.Stale(maxAge)
	lj.mu.Unlock()
	writeJSON(w, map[string]any{"job": lj.Name, "max_age": maxAge.String(), "stale": stale})
}

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

func (s *Server) handleInterim(w http.ResponseWriter, _ *http.Request, lj *liveJob) {
	if lj.Logs {
		writeJSON(w, map[string]any{"job": lj.Name, "interim": []core.BucketResult{}})
		return
	}
	lj.mu.Lock()
	all := lj.eng.Interim()
	lj.mu.Unlock()

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

func (s *Server) handleCloudflareLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	body, err := readLimited(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	points, skipped, err := ingest.ParseLogpush(bytes.NewReader(body))
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	metricJobs := s.metricJobs(ingest.CloudflareMetric)
	anomalies := 0
	for _, lj := range metricJobs {
		anomalies += len(s.pushPoints(r.Context(), lj, points))
	}
	logJobs := s.logJobs()
	if len(logJobs) > 0 {
		lines := ingest.LogpushLines(points)
		for _, lj := range logJobs {
			anomalies += len(s.pushLogs(r.Context(), lj, lines))
		}
	}
	writeJSON(w, map[string]any{
		"accepted": len(points), "skipped": skipped,
		"metric_jobs": len(metricJobs), "log_jobs": len(logJobs), "anomalies": anomalies,
	})
}

func (s *Server) handlePromRemoteWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	body, err := readLimited(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	samples, err := datafeed.ParseRemoteWrite(body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	byMetric := map[string][]core.DataPoint{}
	for _, sm := range samples {
		byMetric[sm.Metric] = append(byMetric[sm.Metric], sm.Point)
	}
	anomalies := 0
	seen := map[*liveJob]bool{}
	for metric, pts := range byMetric {
		for _, lj := range s.metricJobs(metric) {
			anomalies += len(s.pushPoints(r.Context(), lj, pts))
			seen[lj] = true
		}
	}
	writeJSON(w, map[string]any{
		"accepted": len(samples), "metrics": len(byMetric), "jobs": len(seen), "anomalies": anomalies,
	})
}

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
	if len(lj.Spec.Groups) > 0 {
		st["groups"] = lj.Spec.Groups
	}
	if !lj.Last.IsZero() {
		st["last_point"] = lj.Last
	}
	if !lj.Logs {
		st["bucket_span"] = lj.Spec.BucketSpan.String()
		st["detectors"] = lj.Spec.Detectors
		bytes, status := lj.eng.MemoryStatus()
		st["model_bytes"] = bytes
		st["model_memory_status"] = status
	}
	return st
}
