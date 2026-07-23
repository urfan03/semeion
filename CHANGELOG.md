# Changelog

All notable changes to semeion are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.5.0]

A precision-and-correctness hardening pass — measuring, proving, and fixing
rather than adding surface. Three real bugs were found and fixed by the new
tests; all 21 packages green.

### Correctness / robustness
- **Concurrency**: a multi-goroutine ingest+read stress test (deadlock/panic
  guard) and a `make race` target for `-race` where a C toolchain is available.
- **NaN/Inf hardening**: every model family (temporal, distribution,
  multivariate, geo) ignores non-finite input instead of poisoning its baseline
  or emitting non-finite scores.
- **Parser fuzzing** (`go test -fuzz`) for snappy, remote-write protobuf, OTLP,
  Cloudflare and CSV — which **found and fixed a snappy decompression-bomb DoS**
  (a crafted length prefix forced a multi-GB allocation); allocation is now
  bounded and the decoded size capped.
- **Determinism**: identical input now yields byte-identical output — fixed
  nondeterministic influencer tie-breaks (`sort.Slice` → stable + keyed;
  `dominant` map-tie → lexicographic) and stable per-bucket record ordering.
- **Snapshot fixed-point**: a restored engine reproduces the original's
  subsequent results exactly — **fixed drift state (`driftRun`/`driftSign`) not
  being persisted** in ModelState.

### Measuring / proof
- **Score calibration** test: the false-positive rate on stationary noise is
  ~0% at score ≥ 50 (locked in as a regression guard).
- **Golden test**: exact F1 = 1.0 detection on a fixed clear-spike corpus.
- **NAB corpus scoring**: `semeion nab --csv --windows` runs the Numenta
  benchmark on real data; `make bench` / `make bench-nab` targets.

### Scale
- **Benchmarks** (`make bench`): batch and streaming throughput + allocations.
- **Memory-bound test**: under 6000-series cardinality with `MaxSeries=500`,
  resident models stay bounded and eviction fires (no table leaks).

## [0.4.0]

Eight capability additions (each with a regression test; all 21 packages green).
The Go source is comment-free by project convention; docs live in README/CHANGELOG.

### Detection & statistics
- **ratio / SLI detector** (`ratio`, `field` / `denom_field`): scores a per-bucket
  numerator/denominator (error rate, cache-miss %, success ratio) directly.
- **Holt-Winters (ETS) forecasting**: an additive triple-exponential-smoothing
  forecaster (grid-searched α/β/γ) replaces seasonal-naïve when a period is
  present, sharpening the forecast and its bands / breach checks.
- **Ensemble / voting** (`engine.Ensemble`): combines multiple detectors' scores
  for the same series-bucket by Fisher-style tail-probability product, so
  detectors agreeing compounds severity above any single one.

### Operations & reliability
- **Datafeed staleness watchdog** (`Engine.Stale`, `GET /v1/jobs/{name}/stale`):
  reports series that have gone silent relative to the latest bucket — a dead
  exporter that no value detector would catch.
- **Durable result store + history API** (`store.ResultLog`, `GET /v1/history/{job}`,
  `serve --history DIR`): append-only NDJSON log of anomalies, queryable by time
  range, so history survives the in-memory ring.
- **Feedback-driven suppression** (`Engine.MarkFalsePositive`,
  `POST /v1/jobs/{name}/feedback`): marking a series' anomalies as false positives
  raises that series' bar, damping a recurring nuisance without touching others.

### Adoption
- **Job catalog** (`catalog`, `GET /v1/catalog`): ready-made detector sets for
  nginx, kubernetes, postgres, redis, jvm — plug-and-play in one call.
- **OpenAPI spec** (`GET /openapi.json`): the full REST surface as an OpenAPI 3
  document for client generation.

## [0.3.0]

Cloudflare log anomaly detection, a proof-of-quality benchmark, and eleven
capability additions — each with a regression test; all 20 packages green.

### Cloudflare logs

- **Cloudflare Logpush/Logpull ingestion** (`ingest.ParseLogpush`): parse the
  `http_requests` NDJSON firehose (RFC3339 / unix-ns / unix-s timestamps) into
  dimensioned data points — host, path (ids/uuids collapsed to `:id`), method,
  status, status_class, country, colo, cache, waf, client_ip + metrics
  (origin_ms, ttfb_ms, resp_bytes).
- **`ingest.CloudflareJob`**: a ready-made detector set (per-host traffic,
  error-class spike, origin latency, client fan-out, rare country, path
  diversity).
- **`POST /v1/cloudflare/logs`** fans a Logpush body into live "cloudflare"
  metric jobs and categorization jobs; **`semeion cloudflare --file`** CLI.

### Detection & statistics

- **Genuine multi-seasonality**: model two independent cycles (daily AND weekly)
  with an additive back-fitted decomposition, the second period found by
  deflation and admitted only on a ≥10% residual-variance gain (rejects
  harmonics). Persisted across restarts.
- **Regime detection** (`/v1/changepoints`): stable regimes between change points
  + a Welch-based probability that the baseline genuinely shifted (vs a
  transient).
- **Lead-lag / Granger causality** (`stats`, `/v1/leadlag`): which series moved
  first, and whether A's past predicts B — `correlate.OrderByCausality` gives
  data-driven root-cause ordering.
- **`lat_long` detector**: learns a series' typical location (spherical centroid)
  and flags an unusually distant one (impossible-travel / anomalous geo).
- **New detector functions** (from 0.2.0 groundwork): rich detector **rules** —
  `skip_diff_below` / `skip_diff_ratio_below`, `skip_hours_utc` /
  `skip_weekdays_utc`, and `skip_influencer` (safelist by attributed dimension).

### Operations & proof

- **NAB accuracy harness** (`benchmark`): the Numenta Anomaly Benchmark scoring
  model (scaled-sigmoid early-detection reward, application profiles, 0–100
  normalized) + NAB CSV/window loaders — objective, published detection quality.
- **Alert flapping suppression + digest**: an oscillating series is muted after a
  threshold within a window; `alert.Digest` folds low-urgency alerts into one
  periodic summary.
- **Influencer-level aggregation** (`/v1/influencers/{job}`): the "who is
  responsible" roll-up of the entities carrying the most anomalous mass.
- **Model snapshot revert** (`store` versioning; `run --list-snapshots` /
  `--revert`): recover from a training window a one-off event poisoned.
- **Prometheus remote-write receiver** (`POST /v1/prometheus/write`): a push
  datafeed with a hand-rolled Snappy-block + protobuf decoder (no dependency).
- **Grafana SimpleJSON datasource** (`/grafana/search|query|annotations`): point
  a datasource straight at semeion, no custom plugin.

## [0.2.0]

The first end-to-end line is complete: metrics/logs/traces in → anomalies →
incidents → ranked root cause → explanation + recommended fix → error budgets,
in one static binary with no required dependencies.

### Hardening — correctness, statistics, and production (post-audit)

A full adversarial audit (statistics, correlation logic, concurrency, tests)
drove the following fixes; each has a regression test.

**Correctness (were silently producing wrong results):**
- Snapshot/restore now persists **all** model families (seasonal, distribution,
  multivariate, time-of-day) — not just the plain z-score path, which silently
  cold-started the rest on restart.
- A flat baseline (MAD = 0) no longer falls back to the outlier-inflated window
  stddev; it uses a robust, data-relative scale floor, so a past spike can't
  hide a new, smaller anomaly.
- Exponential distribution scoring is two-sided, so a collapse toward zero (an
  outage) is flagged like it is under lognormal/normal.
- Renormalization no longer treats probability-less records (rare / info-content
  / time-of-day) as maximally extreme and pinning them to 100.
- The dependency graph resolves parent/child spans across **separate** OTLP
  batches (per-service collectors) and out of order — it no longer needs both in
  one call to form an edge.
- A change is only credited as a root cause when it **precedes** the incident;
  a mid-incident remediation is never blamed or recommended for rollback.
- **Model memory is bounded** (`model_memory_limit`-equivalent): per-series
  models are LRU-evicted past a cap, so a high-cardinality by/partition/over
  field can't OOM the process; the rare-value map is bounded too.
- Streaming drops late points whose bucket already closed, instead of re-opening
  it into a duplicate, out-of-order result.

**Statistical soundness:** multi-bucket statistic recalibrated (median × √(2M/π),
not √M); z-score detectors are two-sided for both-sided detectors, matching the
distribution detectors; change-point detection scores against a trend model so a
ramp is no longer a staircase of false steps; distribution selection uses AIC
(parameter penalty); multivariate contributions are non-negative and sum to 1;
seasonality detrends before the ACF and anchors phase to the timestamp (bucket
index), not arrival order, so a missing bucket no longer shifts every phase.

**Correlation / SLO:** root-cause confidence is a share of the evidence (not
always 100%); a coarse influencer (`env=prod`) shared by everything no longer
collapses unrelated symptoms into one mega-incident; calendar windows are
excluded from training, not just alerting; incident identity uses an overlap
coefficient so a spreading incident stays one incident; SLO reports **no-data**
distinctly (a dead exporter is not "healthy"), and burn-rate alerting follows
the Google SRE multi-window (fast pair confirmed on a short window).

**Production:** optional bearer-token auth and TLS on `serve`; a universal
request-body cap on every endpoint; optional process-wide rate limiting; webhook
/ Slack secrets redacted from error logs; atomic (temp-file + rename) state
writes; and a corrupt state file no longer blocks startup.

**Parity — closed feature gaps vs Elastic ML:** new detector functions
`distinct_count`, `non_zero_count`, `varp`; **forecast prediction intervals**
(95% bands, `/v1/forecast` + `forecast` CLI); per-influencer **influence score**
(share of the anomalous mass, not just a mode label). Deeper research-scale items
(Prelert-grade Bayesian multi-seasonality, lat_long, and the supervised DFA/NLP
families) remain out of scope by design.

### Precision & functionality (post-audit, each with a regression test)

Ten enhancements that make the engine more precise and more capable:

- **Trend-aware baseline** — a series with a genuine, sustained trend (both
  window-halves agreeing) is scored against its fitted OLS line + residual MAD,
  so steady organic growth no longer reads as a perpetual high anomaly; a level
  *step* still fails the both-halves test and is caught.
- **Per-bucket typical bounds** — every metric record carries `lower`/`upper`
  (the ~95% model band), so a result shows not just the score but the range the
  value was expected to fall in.
- **Warm-up confidence ramp** — scores ramp in over the first buckets past
  warm-up instead of switching on hard, damping cold-start false positives.
- **Concept-drift rebase** — a long, sustained one-directional shift (a new
  normal) trims stale history so the baseline re-centres instead of alerting
  forever on the old level.
- **Adaptive per-series sensitivity** — an optional `sensitivity` (0..1) gates a
  record on its own series' recent score quantile: a chronically noisy series
  must clear its own high-water mark, a quiet one still alerts on a modest bump.
- **New detector functions** — `rate` (per-second event/field rate),
  `non_null_sum`, `metric` (mean summary), and `freq_rare` (rare weighted by
  in-bucket frequency).
- **Gap / missing-bucket handling** — for count-family detectors a missing
  bucket is scored as a real zero (a drop to no-traffic is caught), while metric
  detectors treat a gap as no-data; works in both batch and streaming, bounded
  against sparse-series blow-up.
- **Predictive breach alerting** — `ForecastBreach` projects the forecast bands
  against a threshold/SLO and reports whether and *when* the value will cross it,
  with a probability from the band (exposed via `/v1/forecast` with a
  `threshold`).
- **Interim results** — `Engine.Interim()` (and `GET /v1/jobs/{name}/interim`)
  scores the still-open bucket provisionally (`is_interim`) without closing it or
  disturbing the baseline, for mid-bucket alerting.
- **Categorization depth** — each log category keeps several distinct example
  messages and a cumulative match count, exposed as a category catalogue
  (`Categories()` / `GET /v1/jobs/{name}/categories`).

### Engine (A0–A6)

- **Streaming anomaly detection** — robust baselines (median/MAD, modified
  z-score), single- and multi-metric, `by`/`partition` splits, 0–100 scoring,
  batch and streaming (`Push`/`Flush`), snapshot persistence.
- **Log categorization** — Drain templating with new / rare / spiking template
  detection, population (`over_field`), generic `rare`, influencers, feedback
  suppression.
- **Heavy models** behind a `model.Provider` seam (pure-Go default + optional
  Python plane over HTTP): auto-seasonality and seasonality-aware detection,
  decomposition, forecasting, change-points, Bayesian distribution scoring,
  info-content, time-of-day/week, calendars, rules.
- **True multivariate** (Mahalanobis + χ²) with contribution attribution,
  multi-bucket sustained-shift detection, renormalization, zero-config autopilot.
- **Population outlier detection** — a four-method ensemble (knn / kth-nn / lof /
  ldof) with feature influence; optional pyod plane.
- **Datafeeds** — Prometheus, Elasticsearch, Loki, ClickHouse (pull) and
  OpenTelemetry OTLP/HTTP metrics, logs and traces (push).
- **Alerting** — Slack, generic webhook and Alertmanager sinks, with a score
  floor and bucket-time deduplication.
- **`watch`** — continuous poll → detect → alert → persist, resumable and
  SIGTERM-safe.
- **Live jobs** — resident engines fed by pushed points or OTLP exports.

### Intelligence platform (B1–B3)

- **Correlation** — flattens anomalies into symptoms, groups them by shared
  entity and cross-signal co-occurrence, and ranks root-cause candidates with
  per-candidate reasons. **Change intelligence**: deploys posted to `/v1/changes`
  become the strongest actionable candidate.
- **Topology** — a service dependency graph reconstructed from OTLP traces,
  giving correlation its causal direction (upstream outranks coincident, a call
  path links a cascade across the whole window).
- **Incident lifecycle** — a tracker gives incidents a stable identity: opened
  once, grown in place, escalated on a band crossing, resolved when quiet, with
  one lifecycle alert per transition.
- **Explanation & recommended fix** — a deterministic, evidence-cited brief with
  concrete actions; ships a grounded LLM prompt but no model client (it
  summarises, it never detects).
- **SLO / error budgets** — SRE multi-window multi-burn-rate, ad-hoc and named
  budgets, exhaustion ETA.

### Surfaces & operations

- **REST API** + embedded **Explorer** UI (Anomalies / Incidents / Topology /
  SLO tabs), Grafana endpoint.
- **`/metrics`** — semeion's own operational metrics in Prometheus format.
- **Server state persistence** (`serve --state`) — incidents, dependency graph,
  error budgets and live-job baselines survive a restart.
- **Packaging** — distroless Dockerfile, `docker compose`, Helm chart with an
  optional Python model-plane sidecar.

[Unreleased]: https://github.com/urfan03/semeion/commits/main
