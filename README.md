# semeion

> _sēmeîon_ (σημεῖον), Greek: **the sign that points to a cause** — a symptom.

**semeion** is an open-source, source-agnostic **anomaly detection & correlation engine** —
a free, unlicensed, single-binary alternative to Elastic ML Anomaly Detection.

It doesn't just score anomalies in one signal at a time. It turns every signal
(metrics, logs, traces, deploys) into a stream of **symptoms**, detects what's
statistically unusual, and — layer by layer — correlates those symptoms into a
single explained root cause.

```
Metrics ─┐
Logs ────┤
Traces ──┼─► Anomaly Detection ─► Correlation ─► Root Cause ─► Explanation ─► Fix
Deploys ─┘
```

## Why

- **Elastic ML Anomaly Detection is Platinum-licensed** and needs dedicated ML
  nodes (≥3 for resilience self-hosted). **OpenSearch AD** (Random Cut Forest) is
  metric-only and weak on logs.
- semeion runs from **one static binary**, reads from **any backend**
  (Elasticsearch, OpenSearch, Prometheus, Loki, ClickHouse, Kafka, HTTP push),
  and is **Apache-2.0** — truly open, no SSPL, no node minimum.
- Detection is **deterministic and explainable by design** (classical robust
  statistics + model-based detectors). An LLM, when configured, only *explains*
  the findings — it never *detects*.

## Status

Early development. The core (streaming metric anomaly detection) works today;
see the roadmap below. AccelerUp is the first production consumer.

## Quickstart

```sh
# built-in synthetic demos
go run ./cmd/semeion demo       # metric anomalies
go run ./cmd/semeion catdemo    # log categorization (new / rare / spiking templates)

# quality benchmark (precision / recall / F1 on labelled anomalies)
go run ./cmd/semeion bench

# detect anomalies in a log CSV (time,message[,dims]) — Drain templating
go run ./cmd/semeion logs --file examples/logs.csv

# run a metric job over your own CSV
go run ./cmd/semeion run --job examples/job.json --csv examples/latency.csv

# population: find the host behaving unlike its peers (over_field)
go run ./cmd/semeion run --job examples/population.json --csv examples/population.csv

# seasonality-aware detection: add "seasonal": true to a detector so a value is
# scored against its time-of-cycle baseline (catches a daytime trough / night spike)

# forecast a series (auto-detects the period; seasonal-naive + trend)
go run ./cmd/semeion forecast --csv examples/sine.csv --horizon 12

# ...or route the heavy models through the optional Python plane (statsmodels)
#   (cd python && python server.py 8899) then:
go run ./cmd/semeion forecast --csv examples/sine.csv --model-url http://127.0.0.1:8899

# ...or a Prometheus range query
go run ./cmd/semeion run --job job.json \
  --prom-url http://localhost:9090 --prom-query 'rate(http_requests_total[5m])' \
  --start 2026-01-01T00:00:00Z --end 2026-01-02T00:00:00Z --step 5m

# ...or an Elasticsearch date_histogram (the free path — no ML license)
go run ./cmd/semeion run --job job.json \
  --es-url http://localhost:9200 --es-index 'logs-*' --es-time-field @timestamp \
  --start 2026-01-01T00:00:00Z --end 2026-01-02T00:00:00Z --step 5m

# --state makes a run resumable: baselines are loaded before and saved after
go run ./cmd/semeion run --job job.json --csv data.csv --state ./state.json
```

A job is plain JSON (Elastic-ML-like); `bucket_span` is a Go duration:

```json
{
  "name": "latency-mean",
  "bucket_span": "5m",
  "detectors": [{ "function": "mean", "field": "latency", "side": "high" }]
}
```

The CSV needs a `time` column (RFC3339 or Unix epoch) and the detector's field
column; any other column becomes a dimension for `by`/`partition` splitting.
`--json` emits one anomaly record per line for piping.

## Architecture

Three layers, so the engine stays a clean library and Python is optional:

- **`pkg/`** — pure library: detectors, online statistics, scoring. No I/O.
- **`engine/`** — stateful: buckets points, keeps a model per time-series, emits results.
- **`store/`**, **`ingest/`**, **`api/`**, **`cmd/`** — state, sources, transport, CLI.
- **`model/`** — a `ModelProvider` gRPC contract for the *heavy* model math
  (auto-seasonality, Bayesian distribution fit, change-point, forecasting,
  outlier). Ships with a **native Go provider** (default, zero deps) and an
  **optional Python sidecar** (statsmodels / scipy / ruptures / pyod) for
  research-grade models. The hot streaming path is always pure Go.

## Roadmap

### Engine (this repo)

| Phase | What |
|-------|------|
| **A0 ✅** | Foundation: repo, Apache-2.0, CI, job spec, `Store` interface, benchmark harness |
| **A1 ✅** | Streaming metric AD: robust baseline, single/multi-metric, by/partition, scoring 0–100, bucket span, batch **+ streaming (Push/Flush)**, **snapshot persistence**, **Prometheus + Elasticsearch datafeeds**, CLI, single binary |
| **A2 ✅** | Log **Drain categorization** (new / rare / spiking templates) · **population** (`over_field`) · generic **`rare`** value function · **influencers** (dimension attribution) · **feedback suppression** · categorizer **streaming + snapshot** · `logs`/`catdemo` CLI |
| **A3 ✅** | Heavy models via a `model.Provider` seam (pure-Go default + **live Python plane over HTTP**): **auto-seasonality** + **seasonality-aware detection**, **decompose**, **forecast**, **change-points**, **Bayesian distribution** scoring (normal/lognormal/exponential/poisson), **info-content** (entropy), **time-of-day/week**, **population** + **rare** + **influencers**, **calendars** + **rules/filters**. `forecast`/`logs`/`catdemo` CLIs. |
| **A4** | True multivariate (relationship-break), Shapley attribution, forecasting, multi-bucket, renormalization, model snapshots, zero-config autopilot |
| **A5** | REST/gRPC API, Anomaly Explorer UI + Grafana datasource, more adapters, DFA outlier (pyod), Helm chart |

### Intelligence platform (consumes the engine)

Symptom bus → dependency graph / topology (from traces) + change intelligence →
**correlation engine** (temporal + topological + causal) → root-cause ranking →
AI explanation + recommended fix + SLO/error-budget forecasting.

## License

Apache-2.0. See [LICENSE](./LICENSE).
