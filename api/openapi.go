package api

import "net/http"

func (s *Server) handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, openAPISpec())
}

func op(method, summary string) map[string]any {
	return map[string]any{method: map[string]any{"summary": summary, "responses": map[string]any{"200": map[string]any{"description": "OK"}}}}
}

func merge(ms ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

func openAPISpec() map[string]any {
	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "semeion",
			"description": "Anomaly detection & correlation engine — REST API.",
			"version":     "0.4.0",
		},
		"paths": map[string]any{
			"/v1/analyze":                op("post", "Analyze a batch of points with a job"),
			"/v1/autopilot":              op("post", "Infer a job from points and analyze"),
			"/v1/forecast":               op("post", "Forecast a series (+ bands, optional breach check)"),
			"/v1/changepoints":           op("post", "Change points, regimes, and shift probability"),
			"/v1/leadlag":                op("post", "Lead-lag / Granger causality and RCA ordering"),
			"/v1/outliers":               op("post", "Population outlier scores for a table of rows"),
			"/v1/jobs":                   merge(op("get", "List jobs"), op("post", "Register a live job")),
			"/v1/jobs/{name}":            merge(op("get", "Live-job status"), op("delete", "Remove a live job")),
			"/v1/jobs/{name}/points":     op("post", "Push points/logs into a live job"),
			"/v1/jobs/{name}/flush":      op("post", "Close the open bucket"),
			"/v1/jobs/{name}/interim":    op("get", "Provisional open-bucket scores"),
			"/v1/jobs/{name}/categories": op("get", "Learned log-category catalogue"),
			"/v1/jobs/{name}/stale":      op("get", "Series that have gone silent (staleness)"),
			"/v1/jobs/{name}/feedback":   op("post", "Mark a series' anomalies as false positives"),
			"/v1/results/{job}":          op("get", "Stored bucket results"),
			"/v1/influencers/{job}":      op("get", "Ranked influencers (who is responsible)"),
			"/v1/history/{job}":          op("get", "Durable anomaly history over a time range"),
			"/v1/catalog":                op("get", "List ready-made job templates"),
			"/v1/catalog/{name}":         op("get", "Fetch a job template"),
			"/v1/incidents":              op("get", "Tracked incidents"),
			"/v1/correlate":              op("post", "Correlate caller-supplied symptoms/changes"),
			"/v1/changes":                merge(op("get", "List deploy/config changes"), op("post", "Record a change")),
			"/v1/explain/{id}":           op("get", "Explanation + recommended actions for an incident"),
			"/v1/slo":                    merge(op("get", "List named error budgets"), op("post", "Ad-hoc error-budget report")),
			"/v1/topology":               op("get", "Service dependency graph"),
			"/v1/cloudflare/logs":        op("post", "Ingest Cloudflare Logpush NDJSON"),
			"/v1/prometheus/write":       op("post", "Prometheus remote-write receiver"),
			"/v1/otlp/v1/metrics":        op("post", "OTLP/HTTP metrics export"),
			"/v1/otlp/v1/logs":           op("post", "OTLP/HTTP logs export"),
			"/v1/otlp/v1/traces":         op("post", "OTLP/HTTP trace export"),
			"/v1/forecasts":              merge(op("get", "List active forecasts"), op("post", "Create a persisted forecast")),
			"/v1/forecasts/{id}":         merge(op("get", "Fetch a forecast"), op("delete", "Delete a forecast")),
			"/v1/filters":                op("get", "List reusable filter lists"),
			"/v1/filters/{id}":           merge(op("get", "Fetch a filter list"), op("put", "Create/replace a filter list"), op("delete", "Delete a filter list")),
			"/v1/cluster":                op("get", "Cluster membership and this node's identity"),
			"/grafana/search":            op("post", "Grafana SimpleJSON: targets"),
			"/grafana/query":             op("post", "Grafana SimpleJSON: query"),
			"/grafana/annotations":       op("post", "Grafana SimpleJSON: annotations"),
			"/metrics":                   op("get", "Prometheus metrics for semeion itself"),
			"/healthz":                   op("get", "Liveness check"),
		},
	}
}
