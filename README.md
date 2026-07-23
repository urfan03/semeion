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

# zero-config: let semeion infer a job (bucket span + detectors) from your data
go run ./cmd/semeion autopilot --csv examples/latency.csv

# population outliers: which entity doesn't belong, and because of which column
go run ./cmd/semeion outliers --csv examples/hosts.csv

# multivariate: a job detector with "fields": ["cpu","mem","io"] flags a broken
# correlation between metrics even when each one alone is in range

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

# server: REST API + the built-in Anomaly Explorer UI on http://localhost:8080
go run ./cmd/semeion serve --demo

# ...persist everything across restarts (incidents, graph, budgets, baselines)
go run ./cmd/semeion serve --state ./semeion-state.json

# ...locked down: bearer-token auth, TLS, and a request rate cap
go run ./cmd/semeion serve --auth-token "$TOKEN" --rate-limit 200 \
  --tls-cert cert.pem --tls-key key.pem

# production mode: poll a live source forever and alert on what it finds
go run ./cmd/semeion watch --job job.json \
  --prom-url http://localhost:9090 --prom-query 'histogram_quantile(0.95, rate(http_latency_bucket[5m]))' \
  --interval 1m --state ./state.json --slack-webhook "$SLACK_URL"
```

### Incidents: from anomalies to a cause

Detection tells you *what* is unusual. On a real system that is forty records at
once — one per service, per metric, per log template — and a human still has to
work out that they are **one event with one cause**. `GET /v1/incidents` does
that step:

```
2 symptoms across 2 job(s), 1 change(s) over 10m24s
  — likely origin: checkout v2.3.1 (deliberate change)
    1.00  checkout v2.3.1   [deliberate change, first event of the incident]
    0.81  checkout-errors   [early (120s after the start)]
    0.64  cart-latency      [part of the incident]
```

Symptoms are linked by **single-link clustering** under two rules: a shared
entity (same host, same service — taken from the detector's influencers) links
across the full window; without one, only *different* signals link, and only
within a tighter co-occurrence window. That asymmetry matters — two records from
one noisy job on different hosts are two problems, not one incident, while two
different services degrading at the same second usually share an upstream cause.

Root cause is ranked on four weighted beliefs, and every candidate carries the
reasons it was placed where it was: **a cause precedes its effects** (0.40), **a
deliberate change beats a measurement** (0.30), severity relative to the
incident (0.15), and how much of the incident's entity surface the symptom
covers (0.15). Post your deploys to `/v1/changes` from CI and the correlation
has the one piece of evidence no metric contains. A change that lands *after*
the symptoms is ranked down, not up — the engine will not credit a deploy with
causing something that preceded it.

No model and no LLM produces this — it is deterministic and arguable. An LLM can
summarise the result; it must not be what generates it.

#### Topology: which service *could* have caused it

Time and co-occurrence only say things happened together. Feed traces to
`POST /v1/otlp/v1/traces` and semeion builds the service dependency graph from
the spans (never a hand-maintained list — that is wrong within a week), then
correlation gains a causal direction:

- a caller and its callee failing together are **one** incident, however far
  apart in the window — that is the shape of a cascade;
- the service the *others depend on* is ranked as the origin, and coincidence
  cannot manufacture a call path.

```
2 → 3 tiers, gateway noticed first (score 95), payments-db last (score 65):
  1.00  payments-db   [upstream of 2 of the 2 other affected services]
  0.89  checkout      [upstream of 1 of the 2 other affected services]
  0.78  gateway       [first event of the incident]
```

The database is blamed even though it lit up **last and mildest** — the
user-facing metric is the sensitive one, so order of first detection is an
artifact of detector sensitivity, while the call graph is not. That is why
dependency (0.35) outweighs earliness (0.25) in the ranking. A deliberate change
still leads when there is one — it is the thing a human can revert.

#### Lifecycle: one incident, not one per poll

`Correlate` is stateless — call it twice on overlapping data and it returns two
fresh sets with no memory that they describe the same ongoing event. The
**tracker** closes that gap: it matches each freshly correlated incident to an
open one by entity overlap (overlap coefficient ≥ 0.5, stable as it spreads), so a persistent incident is

- **opened once** and alerted once — not re-paged on every poll;
- grown **in place** (its id is stable; new services can join without spawning a
  duplicate);
- **escalated** only when its peak score crosses a severity band (warning →
  critical), not on every point of drift;
- **resolved** when no fresh symptom has arrived for `ResolveAfter` (15m).

`GET /v1/incidents` returns this tracked view; `/open` and `/resolved` return the
sets directly. On the live-ingest path the tracker reconciles automatically as
data flows, so incidents open and resolve on their own — and each transition
fires one lifecycle alert (`opened` / `escalated` / `resolved`) through the same
Slack / webhook / Alertmanager sinks, bypassing the per-series dedup because the
tracker has already deduplicated the event itself.

#### Explanation & recommended fix

`GET /v1/explain/{id}` turns a ranked incident into a brief an on-call engineer
can act on — deterministically, from the evidence the engine already has:

```
Likely caused by checkout v2.3.1 (deploy) — affecting checkout, cart
  1. Roll back checkout v2.3.1
     — it is the deliberate change most closely preceding the incident,
       and is the fastest thing to reverse
```

Every sentence and every action **cites the evidence that produced it** — a
rollback because a change led the ranking, "investigate the upstream service"
because the topology said so, "inspect the new log pattern" quoting the template
that first appeared. If there is no ranked cause it says so; it never invents a
service, metric, or cause the incident didn't contain.

An LLM may rewrite the narrative into nicer prose — the response includes a
ready `prompt` whose preamble forbids adding findings and carries the
already-decided actions, so a summariser can't quietly become a (bad) detector.
semeion ships **no** model client: the deterministic brief is always the source
of truth, and the prompt is the seam a copilot plugs into.

### SLO & error budgets

`POST /v1/slo` answers the forward-looking question: given an objective and a
stream of good/total counts, how much reliability budget is left and — at the
current burn — when does it run out? The maths is the standard Google SRE
multi-window, multi-burn-rate model:

```
SLI 0.9500 · budget consumed 50.0× · burn 50.0× · severity critical
```

The full window drives budget consumption; short trailing windows drive burn-
rate alerting, so a budget that looks fine on the month still pages when it
starts draining fast in the last half hour — and a 30-minute spike is flagged
without being mistaken for a blown monthly budget. When something is burning but
intact, the report projects the exhaustion time.

Named budgets (`POST /v1/slo/{name}`) accumulate samples over time into a rolling
window trimmed to the objective — so a service pushes good/total counts as it
runs and `GET /v1/slo` gives the live board the SLO tab renders. `POST /v1/slo`
without a name stays a stateless one-shot evaluator.

### Outliers: the population question

The engine asks "is this point unusual *for this series, now*". `outliers` asks
the other question — "is this row unusual *among its peers*" — which is what you
want for a snapshot of 400 hosts, 200 endpoints, or every customer's usage.
(Elastic charges for this as Data Frame Analytics outlier detection.)

```
$ semeion outliers --csv examples/hosts.csv --top 3
26 rows × 4 features (cpu, mem, io_wait, latency_ms)

  cache-06                 score=0.955  ← latency_ms 89%, cpu 7%
  web-13                   score=0.948  ← io_wait 99%, mem 0%
  db-07                    score=0.872  ← cpu 100%, io_wait 0%
```

Four unsupervised methods vote — `knn`, `kth_nn` (global isolation), `lof`,
`ldof` (local density) — because no single one is right everywhere: distance
methods flag legitimately sparse regions, density methods catch a row sitting
*between* two clusters. Each method's raw score is normalized through a robust
logistic in log space, so **0.5 means "3 robust deviations above the
population"** for every method and averaging them is meaningful. In the example
above the three planted hosts rise to the top while the genuinely distinct
`cache-*` group stays below 0.5 — a different cluster is not an outlier.

Every score carries **feature influence** (an exact decomposition of the squared
distance, summing to 1), because "host-7 is an outlier" is useless without
"…because of `io_wait`". Available as `POST /v1/outliers` too, where rows are
plain objects: numeric fields are features, string fields are labels echoed
back. With the Python plane enabled the scoring is delegated to pyod, falling
back to the Go ensemble on any error.

### Alerting

`watch` polls, scores, and routes anomalies to any combination of
`--slack-webhook`, `--webhook` (raw JSON), and `--alertmanager` (v2 API — the
anomaly lands in your existing routing, silencing and on-call rules). With no
sink configured it prints, so a run is never silently doing nothing.

Two guards keep a sustained anomaly from paging every bucket:

- `--min-score` (defaults to `--threshold`) — the score floor.
- `--dedup` (default `30m`) — one alert per `(job, detector, series)` per
  window, measured in **bucket time**, so replaying history alerts exactly like
  a live run would.

The newest bucket is never alerted on until a later bucket arrives — a partial
bucket is not an anomaly.

### Server

`serve` exposes the engine over HTTP and serves the embedded **Explorer** (no
build step, no CDN — the UI is `go:embed`ed in the binary). It has four tabs:
**Anomalies** (the swimlane + records for a job), **Incidents** (correlated
incidents with ranked root cause and one-click *Explain & recommend*),
**Topology** (the trace-derived dependency graph), and **SLO** (named error
budgets with their burn and severity). Endpoints:

| Endpoint | What |
|----------|------|
| `POST /v1/analyze` | `{"job": {...}, "points": [...]}` → bucket results; the run is kept under its job name |
| `POST /v1/jobs` | register a **live job** — its engine stays resident and scores what you push |
| `GET/DELETE /v1/jobs/{name}` | live-job status / removal |
| `POST /v1/jobs/{name}/points` | push points (or `logs`) into a live job → the anomalies they produced |
| `POST /v1/jobs/{name}/flush` | close the still-open bucket (end of a backfill, or a low-traffic job) |
| `GET /v1/jobs/{name}/interim` | provisional (`is_interim`) scores for the still-open bucket — mid-bucket, without closing it |
| `GET /v1/jobs/{name}/categories` | learned log-category catalogue (id, template, example messages, match counts) |
| `POST /v1/otlp/v1/metrics` | **OTLP/HTTP** metrics export → routed to the live jobs that claim each metric |
| `POST /v1/otlp/v1/logs` | OTLP/HTTP logs export → fed to every live categorization job |
| `POST /v1/cloudflare/logs` | **Cloudflare Logpush/Logpull** NDJSON → dimensioned points fanned into "cloudflare" metric + categorization jobs |
| `POST /v1/prometheus/write` | **Prometheus remote-write** (Snappy+protobuf) → samples fanned into the live jobs that claim each metric |
| `POST /v1/changepoints` | `{"series":[...]}` → change points, stable **regimes**, and the probability the baseline **shifted** |
| `POST /v1/leadlag` | lead-lag + **Granger** causality: pairwise (`a`/`b`) or rank `candidates` by causal lead over a `target` (RCA ordering) |
| `GET /v1/influencers/{job}` | ranked **influencers** — the entities carrying the most anomalous mass (optional `?field=`) |
| `GET /v1/jobs/{name}/interim` · `/categories` | provisional open-bucket scores · learned log-category catalogue |
| `GET /v1/incidents` | tracked incidents (stable ids, open/resolved status); `?stateless=1` for a fresh one-shot correlation |
| `GET /v1/incidents/open` · `/resolved` | the tracked open / recently-resolved sets |
| `POST /v1/correlate` | correlate a caller-supplied set of symptoms/changes (one-shot, untracked) |
| `GET/POST /v1/changes` | the deploy / config-change log incidents are correlated against |
| `GET /v1/explain/{id}` | deterministic explanation + recommended actions for an incident, plus a grounded LLM prompt |
| `POST /v1/slo` | ad-hoc error-budget report from posted good/total samples |
| `GET/POST /v1/slo/{name}` | a **named** error budget: append samples over time, read the standing report |
| `GET /v1/slo` | list named error budgets with their current SLI / burn / severity |
| `POST /v1/otlp/v1/traces` | OTLP/HTTP trace export → folded into the service dependency graph |
| `GET /v1/topology` | the dependency graph (nodes + edges) correlation reasons over |
| `POST /v1/outliers` | a table of rows → population outlier scores + feature influence |
| `POST /v1/autopilot` | points in → an inferred job + its results |
| `POST /v1/forecast` | `{"series": [...], "horizon": N}` → point forecast **+ 95% prediction bands**; add `"threshold": X` (+ optional `"side": "low"`) for a **predictive breach** check — will/when the value crosses the limit (uses the model plane if configured) |
| `GET /v1/jobs` | analysed job names |
| `GET /v1/results/{job}` | stored bucket results |
| `GET /v1/grafana/{job}` | flat `time`/`score`/`detector`/`kind` rows for a Grafana table/time-series panel |
| `GET /metrics` | semeion's **own** metrics in Prometheus format (live jobs, open incidents, per-SLO burn) — scrapeable by the same Prometheus it reads from |
| `GET /healthz` | liveness |
| `GET /` | Anomaly Explorer UI |

With `serve --state FILE`, the full server state — the change log, dependency
graph, tracked incidents (ids and all), named error budgets, and every live
job's learned baseline — is snapshotted to a JSON file periodically and on
`SIGTERM`, and restored on the next start. A restart resumes its incidents and
keeps its models instead of re-warming; a missing or corrupt file just starts
empty (it never blocks startup).

**Hardening.** `serve` takes an optional bearer token (`--auth-token`, or
`$SEMEION_AUTH_TOKEN`) required on every endpoint except `/healthz` and
`/metrics`; TLS (`--tls-cert`/`--tls-key`); and a process-wide request rate cap
(`--rate-limit`). Every request body is size-capped so an unauthenticated POST
can't exhaust memory, per-series models are LRU-bounded so a high-cardinality
field can't OOM the process, and webhook/Slack secrets are redacted from logs.

### Push instead of poll (OpenTelemetry)

`watch` pulls; the server also accepts **pushes**. Register a live job, then
point an OTel Collector at semeion — no SDK dependency, just OTLP/JSON:

```sh
curl -XPOST localhost:8080/v1/jobs -d '{
  "job": {"name": "checkout-latency", "bucket_span": "1m",
          "detectors": [{"function": "mean", "field": "value", "side": "high"}]},
  "metric": "http.server.duration"}'
```

```yaml
# otel-collector.yaml
exporters:
  otlphttp/semeion:
    endpoint: http://semeion:8080/v1/otlp
    encoding: json
```

The same collector export also carries **traces** to `/v1/otlp/v1/traces`, which
build the dependency graph (see Incidents → Topology) — metrics, logs and traces
over one endpoint.

Resource **and** point attributes become dimensions, so `by`/`partition`
splitting on `service.name`, `host` or `route` works with no extra config.
Histograms are scored on their mean, `asInt` string-encoded int64s are handled,
and a metric no job claims is accepted and ignored. Run `serve` with
`--slack-webhook` / `--webhook` / `--alertmanager` and live jobs alert through
the same sinks as `watch`.

```sh
docker compose up            # engine + optional Python model plane
helm install semeion deploy/helm/semeion \
  --set modelPlane.enabled=true          # scipy/statsmodels sidecar, off by default
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
- **`outlier/`** — batch, cross-sectional: scores a table of entities against
  each other (no time axis), with feature influence.
- **`correlate/`** — groups anomalies (and deploys) into incidents and ranks
  root-cause candidates. Deterministic and explainable, never a model.
- **`topology/`** — the service dependency graph, reconstructed from OTLP
  traces; gives correlation its causal direction (who is upstream of whom).
- **`explain/`** — turns a ranked incident into an evidence-cited brief and
  recommended actions; produces the LLM prompt but never calls a model.
- **`slo/`** — error-budget attainment and multi-window burn-rate forecasting.
- **`datafeed/`**, **`otlp/`**, **`api/`**, **`alert/`**, **`cmd/`** — pull
  sources (Prometheus, Elasticsearch, Loki, ClickHouse, CSV), the OTLP/JSON push
  decoder, the REST transport + embedded UI, the outbound sinks, and the CLI.
  Detection never performs I/O; `alert/` is the only package that talks to
  Slack, a webhook, or Alertmanager.
- **`model/`** — a `ModelProvider` JSON/HTTP contract for the *heavy* model math
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
| **A4 ✅** | **True multivariate** (relationship-break, Mahalanobis + χ²) + **contribution attribution**, **multi-bucket** (sustained-shift detection, median-robust), **renormalization** (rescale relative to the biggest anomaly), **zero-config autopilot** (infer a job from data), per-field metrics from `Values`. |
| **A5 ✅** | **Ecosystem**: REST API (`serve`) + embedded **Anomaly Explorer** UI + **Grafana** endpoint, **Loki** + **ClickHouse** datafeeds, distroless **Dockerfile** + `docker compose`, **Helm chart** with an optional Python model-plane sidecar |
| **A6 🚧** | **Alerting** (`alert`: Slack / webhook / Alertmanager sinks, score floor + bucket-time dedup) ✅ · **`watch`** continuous mode (poll → detect → alert → persist, resumable, SIGTERM-safe) ✅ · **live jobs + OTLP/HTTP ingestion** (push metrics *and* logs straight from an OTel Collector) ✅ · **population outlier detection** (4-method ensemble + feature influence, `outliers` CLI + API, optional pyod plane) ✅ · Kafka ingestion, gRPC API — pending |
| **Hardening ✅** | Full post-audit pass: 8 correctness fixes (snapshot-all-models, robust flat-baseline, exponential dips, renormalization, cross-batch topology, change-precedence, model-memory LRU, out-of-order guard), statistical recalibration (multi-bucket, two-sided, change-point trend, AIC, seasonality detrend + calendar-phase), correlation/SLO fixes (confidence share, coarse-influencer guard, calendar training-exclusion, no-data SLO, MWMBR burn, incident-identity), production (auth/TLS/rate-limit/body-cap/secret-redaction/atomic-state), and parity additions (`distinct_count`/`non_zero_count`/`varp`, forecast bands, influence scores). See CHANGELOG. |
| **Precision ✅** | Ten precision/functionality upgrades (each with a regression test): **trend-aware baseline** (fitted-line scoring for genuine trends, steps still caught), **per-bucket typical bounds** (`lower`/`upper`), **warm-up confidence ramp**, **concept-drift rebase**, **adaptive per-series sensitivity** (`sensitivity`), new functions **`rate`/`non_null_sum`/`metric`/`freq_rare`**, **gap / missing-bucket** zero-fill for count-family (batch + streaming), **predictive breach** alerting (forecast × threshold/SLO), **interim** open-bucket results (`is_interim`, `/v1/jobs/{name}/interim`), and **categorization depth** (multiple examples + match counts, `/v1/jobs/{name}/categories`). See CHANGELOG. |
| **Reach ✅** | **Cloudflare** log anomaly detection (Logpush/Logpull → `ingest.CloudflareJob`, `/v1/cloudflare/logs`, `semeion cloudflare`); **genuine multi-seasonality** (daily + weekly, deflation + variance-gain); **regime detection** + **lead-lag/Granger** causality for RCA ordering (`/v1/changepoints`, `/v1/leadlag`); **`lat_long`** geo detector; **rich detector rules** (diff-ratio / hour / influencer safelists); **NAB** accuracy harness; alert **flapping suppression + digest**; **influencer aggregation** (`/v1/influencers`); model-**snapshot revert** (`run --revert`); **Prometheus remote-write** receiver; **Grafana SimpleJSON** datasource. See CHANGELOG. |

### Intelligence platform (consumes the engine)

| Phase | What |
|-------|------|
| **B1 ✅** | **Correlation engine**: symptom flattening (influencers → entities), single-link grouping (shared entity / cross-signal co-occurrence), **change intelligence** (`/v1/changes` — deploys from CI), weighted root-cause ranking with per-candidate reasons; `GET /v1/incidents`, `POST /v1/correlate` |
| **B2 ✅** | **Dependency graph / topology from traces** (`topology` — built from OTLP spans, `/v1/otlp/v1/traces` + `/v1/topology`) → *topological* correlation: a call path links a cascade across the whole window, and the upstream service outranks the coincident one · **incident lifecycle** (`correlate.Tracker` — overlap-matched identity, open once / grow in place / escalate on band crossing / resolve when quiet) with **lifecycle alerting** through the existing sinks |
| **B3 ✅** | **Explanation + recommended fix** (`explain` — deterministic brief with evidence-cited actions, `/v1/explain/{id}`; ships a grounded LLM prompt but no model client — summarises, never detects) · **SLO / error-budget forecasting** (`slo` — SRE multi-window multi-burn-rate, named budgets `/v1/slo/{name}`, exhaustion ETA) · **4-tab Explorer** (Anomalies / Incidents / Topology / SLO) · **server state persistence** (`serve --state` — snapshot & restore incidents, graph, budgets, live-job baselines) |

## License

Apache-2.0. See [LICENSE](./LICENSE).
