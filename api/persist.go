package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/urfan03/semeion/correlate"
	"github.com/urfan03/semeion/engine"
	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/logcat"
	"github.com/urfan03/semeion/slo"
	"github.com/urfan03/semeion/topology"
)

// stateVersion guards against loading a snapshot from an incompatible build.
const stateVersion = 1

// ServerState is everything a restart would otherwise lose: the intelligence
// state (changes, dependency graph, incident tracker, error budgets) and the
// live jobs with their learned baselines. Detection results are not persisted —
// they are cheap to recompute and bounded in memory already.
type ServerState struct {
	Version  int                       `json:"version"`
	SavedAt  time.Time                 `json:"saved_at"`
	Changes  []correlate.Change        `json:"changes,omitempty"`
	Graph    topology.Snapshot         `json:"graph"`
	Tracker  correlate.TrackerSnapshot `json:"tracker"`
	SLOs     map[string]sloSnapshot    `json:"slos,omitempty"`
	LiveJobs []liveJobSnapshot         `json:"live_jobs,omitempty"`
}

type sloSnapshot struct {
	Target  slo.Target   `json:"target"`
	Samples []slo.Sample `json:"samples"`
}

// liveJobSnapshot persists a live job's definition and its learned baselines, so
// after a restart it re-registers and keeps its model instead of re-warming.
type liveJobSnapshot struct {
	Name       string           `json:"name"`
	Metric     string           `json:"metric,omitempty"`
	Threshold  float64          `json:"threshold"`
	Logs       bool             `json:"logs"`
	Spec       jobspec.Job      `json:"spec"`
	BucketSpan string           `json:"bucket_span,omitempty"`
	Engine     *engine.Snapshot `json:"engine,omitempty"`
	Cat        *logcat.Snapshot `json:"cat,omitempty"`
	Points     int              `json:"points"`
	Created    time.Time        `json:"created"`
	Last       time.Time        `json:"last"`
}

// Snapshot captures the full server state.
func (s *Server) Snapshot() ServerState {
	st := ServerState{
		Version: stateVersion,
		SavedAt: time.Now().UTC(),
		Graph:   s.graph.Snapshot(),
		Tracker: s.tracker.Snapshot(),
		SLOs:    map[string]sloSnapshot{},
	}
	s.mu.RLock()
	st.Changes = append([]correlate.Change(nil), s.changes...)
	for name, series := range s.slos {
		series.mu.Lock()
		st.SLOs[name] = sloSnapshot{Target: series.Target, Samples: append([]slo.Sample(nil), series.samples...)}
		series.mu.Unlock()
	}
	jobs := make([]*liveJob, 0, len(s.live))
	for _, lj := range s.live {
		jobs = append(jobs, lj)
	}
	s.mu.RUnlock()

	for _, lj := range jobs {
		lj.mu.Lock()
		snap := liveJobSnapshot{
			Name: lj.Name, Metric: lj.Metric, Threshold: lj.Threshold, Logs: lj.Logs,
			Spec: lj.Spec, Points: lj.Points, Created: lj.Created, Last: lj.Last,
		}
		if lj.eng != nil {
			es := lj.eng.Snapshot()
			snap.Engine = &es
		}
		if lj.cat != nil {
			cs := lj.cat.Snapshot()
			snap.Cat = &cs
			snap.BucketSpan = cs.Span.String() // for the (unlikely) nil-cat restore path
		}
		lj.mu.Unlock()
		st.LiveJobs = append(st.LiveJobs, snap)
	}
	return st
}

// Restore rebuilds server state from a snapshot. It is meant to run once, on a
// fresh server, before it starts serving.
func (s *Server) Restore(st ServerState) error {
	if st.Version != stateVersion {
		return fmt.Errorf("state version %d unsupported (want %d)", st.Version, stateVersion)
	}
	s.graph.Restore(st.Graph)
	s.tracker.Restore(st.Tracker)

	s.mu.Lock()
	s.changes = append([]correlate.Change(nil), st.Changes...)
	s.slos = map[string]*sloSeries{}
	for name, snap := range st.SLOs {
		s.slos[name] = &sloSeries{Target: snap.Target, samples: snap.Samples}
	}
	s.mu.Unlock()

	for _, snap := range st.LiveJobs {
		lj := &liveJob{
			Name: snap.Name, Metric: snap.Metric, Threshold: snap.Threshold, Logs: snap.Logs,
			Spec: snap.Spec, Points: snap.Points, Created: snap.Created, Last: snap.Last,
		}
		if snap.Logs {
			span, err := parseSpan(snap.BucketSpan)
			if err != nil {
				return fmt.Errorf("live job %q: %w", snap.Name, err)
			}
			cat := logcat.NewCategorizer(span)
			if snap.Cat != nil {
				cat = logcat.RestoreCategorizer(*snap.Cat)
			}
			lj.cat = cat
		} else {
			eng, err := engine.NewWithProvider(snap.Spec, s.provider)
			if err != nil {
				return fmt.Errorf("live job %q: %w", snap.Name, err)
			}
			if snap.Engine != nil {
				eng.Restore(*snap.Engine)
			}
			lj.eng = eng
		}
		s.mu.Lock()
		s.live[lj.Name] = lj
		s.mu.Unlock()
	}
	return nil
}

// SaveState writes the server state to path (atomically, via a temp file).
func (s *Server) SaveState(path string) error {
	raw, err := json.Marshal(s.Snapshot())
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadState restores server state from path. A missing file is not an error —
// a first run simply starts empty. The bool reports whether a file was loaded.
func (s *Server) LoadState(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var st ServerState
	if err := json.Unmarshal(raw, &st); err != nil {
		return false, fmt.Errorf("state file %s: %w", path, err)
	}
	return true, s.Restore(st)
}
