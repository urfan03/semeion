package api

import (
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/correlate"
	"github.com/urfan03/semeion/otlp"
	"github.com/urfan03/semeion/slo"
)

// SeedIntelligenceDemo populates the incident, topology and SLO stores with one
// coherent scenario, so a fresh `serve --demo` shows every tab with real data:
// a checkout deploy, a cascade from payments-db up through checkout, the trace
// graph that explains it, and two error budgets — one healthy, one burning.
//
// It uses the same public shapes any client would; nothing here is special-cased
// by the detection or correlation code.
func (s *Server) SeedIntelligenceDemo(now time.Time) {
	// A cascade: the database degrades, checkout errors, both within a minute.
	spike := func(job, service string, offset time.Duration, score float64) {
		ts := now.Add(offset)
		s.Store(job, []core.BucketResult{{
			Time: ts, Score: score,
			Records: []core.Record{{
				Time: ts, Detector: "mean(latency)", Series: service, Score: score,
				Actual: 900, Typical: 120, Probability: 1e-7, Direction: core.DirUp, Kind: "metric",
				Influencers: []core.Influencer{{Field: "service", Value: service}},
			}},
		}})
	}
	spike("demo-db-latency", "payments-db", -1*time.Minute, 72)
	spike("demo-checkout-errors", "checkout", -30*time.Second, 95)

	// The deploy that most likely started it.
	s.RecordChange(correlate.Change{
		Time: now.Add(-2 * time.Minute), Name: "checkout v2.3.1", Kind: "deploy",
		Labels: map[string]string{"service": "checkout"},
	})

	// The dependency graph: gateway → checkout → payments-db.
	base := now.Add(-90 * time.Second)
	span := func(trace, id, parent, service string, ms int) otlp.Span {
		return otlp.Span{TraceID: trace, SpanID: id, ParentID: parent, Service: service,
			Start: base, End: base.Add(time.Duration(ms) * time.Millisecond)}
	}
	s.graph.Observe([]otlp.Span{
		span("d1", "a", "", "gateway", 300),
		span("d1", "b", "a", "checkout", 260),
		span("d1", "c", "b", "payments-db", 200),
	})

	// Two error budgets: one comfortable, one draining.
	demoSLO := func(name string, objective, errRatio float64) {
		series := s.sloSeries(name, true)
		series.mu.Lock()
		series.Target = slo.Target{Objective: objective, Window: 24 * time.Hour}
		series.mu.Unlock()
		samples := make([]slo.Sample, 1440)
		for i := range samples {
			samples[i] = slo.Sample{Time: now.Add(-time.Duration(1440-i) * time.Minute),
				Total: 1000, Good: 1000 * (1 - errRatio)}
		}
		series.append(samples)
	}
	demoSLO("demo-availability", 0.999, 0.0004) // ~40% budget used — healthy
	demoSLO("demo-payments", 0.999, 0.02)       // blown — critical

	// Open the incident now so the Incidents tab is populated on first load.
	incidents, _ := s.correlateAll(correlate.Options{Window: 10 * time.Minute})
	s.tracker.Reconcile(incidents)
}
