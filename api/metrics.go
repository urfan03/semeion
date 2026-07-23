package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	var b strings.Builder
	m := &promWriter{b: &b}

	s.mu.RLock()
	metricJobs, logJobs, points := 0, 0, 0
	jobPoints := map[string]int{}
	for name, lj := range s.live {
		lj.mu.Lock()
		if lj.Logs {
			logJobs++
		} else {
			metricJobs++
		}
		points += lj.Points
		jobPoints[name] = lj.Points
		lj.mu.Unlock()
	}
	changes := len(s.changes)
	sloNames := make([]string, 0, len(s.slos))
	for k := range s.slos {
		sloNames = append(sloNames, k)
	}
	s.mu.RUnlock()

	m.gauge("semeion_build_info", "Build/version marker (always 1).", 1, lbl("version", version))
	m.help("semeion_live_jobs", "Resident live jobs by kind.", "gauge")
	m.line("semeion_live_jobs", float64(metricJobs), lbl("kind", "metric"))
	m.line("semeion_live_jobs", float64(logJobs), lbl("kind", "log"))

	m.help("semeion_job_points_ingested", "Points/lines ingested per live job.", "counter")
	sort.Strings(sloNames)
	for _, name := range sortedKeys(jobPoints) {
		m.line("semeion_job_points_ingested", float64(jobPoints[name]), lbl("job", name))
	}

	m.gauge("semeion_changes", "Recorded deploy/config changes in the log.", float64(changes))
	m.gauge("semeion_alerts_sent_total", "Alerts delivered to sinks since start.", float64(s.alertsSent.Load()))

	open := s.tracker.Open()
	crit := 0
	for _, o := range open {
		if o.PeakScore >= 75 {
			crit++
		}
	}
	m.gauge("semeion_incidents_open", "Currently open incidents.", float64(len(open)))
	m.gauge("semeion_incidents_open_critical", "Open incidents in the critical band.", float64(crit))
	m.gauge("semeion_incidents_resolved", "Recently resolved incidents retained.", float64(len(s.tracker.Resolved())))

	nodes, edges := s.graph.Nodes(), s.graph.Edges()
	edgeErrors := 0
	for _, e := range edges {
		edgeErrors += e.Errors
	}
	m.gauge("semeion_topology_services", "Services in the dependency graph.", float64(len(nodes)))
	m.gauge("semeion_topology_edges", "Directed call edges in the dependency graph.", float64(len(edges)))
	m.gauge("semeion_topology_edge_errors", "Errored calls summed across edges.", float64(edgeErrors))

	now := time.Now().UTC()
	m.help("semeion_slo_sli", "Observed SLI per named SLO.", "gauge")
	m.help("semeion_slo_budget_consumed", "Error-budget fraction consumed (>1 = blown).", "gauge")
	m.help("semeion_slo_burn_rate", "Current error-budget burn rate (×).", "gauge")
	for _, name := range sloNames {
		series := s.sloSeries(name, false)
		if series == nil {
			continue
		}
		rep := series.evaluate(now)
		l := lbl("slo", name)
		m.line("semeion_slo_sli", rep.SLI, l)
		m.line("semeion_slo_budget_consumed", rep.BudgetConsumed, l)
		m.line("semeion_slo_burn_rate", rep.BurnRate, l)
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

var version = "dev"

type label struct{ name, value string }

func lbl(name, value string) label { return label{name, value} }

type promWriter struct {
	b    *strings.Builder
	seen map[string]bool
}

func (w *promWriter) help(name, help, typ string) {
	if w.seen == nil {
		w.seen = map[string]bool{}
	}
	if w.seen[name] {
		return
	}
	w.seen[name] = true
	fmt.Fprintf(w.b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
}

func (w *promWriter) gauge(name, help string, v float64, labels ...label) {
	w.help(name, help, "gauge")
	w.line(name, v, labels...)
}

func (w *promWriter) line(name string, v float64, labels ...label) {
	w.b.WriteString(name)
	if len(labels) > 0 {
		w.b.WriteByte('{')
		for i, l := range labels {
			if i > 0 {
				w.b.WriteByte(',')
			}
			fmt.Fprintf(w.b, "%s=\"%s\"", l.name, escapeLabel(l.value))
		}
		w.b.WriteByte('}')
	}
	fmt.Fprintf(w.b, " %s\n", formatFloat(v))
}

func escapeLabel(s string) string {
	return strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n").Replace(s)
}

func formatFloat(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%g", v)
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
