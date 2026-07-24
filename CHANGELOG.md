# Changelog

All notable changes to semeion are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.9.0]

Production-readiness hardening. A four-agent audit (quality gate, server
security/concurrency, algorithmic correctness, deploy/integration) drove this
release; every confirmed finding is fixed with a regression test and all 22
packages are green.

### Correctness
- **Renormalization ranking fix**: `RenormalizeResults` now gives `sevOf` the
  same `1e-12` probability floor that `probFromScore`/`Ensemble` already use, so
  a maximally-extreme anomaly whose tail probability underflowed to 0 can no
  longer be renormalized *below* a less-extreme one on the opt-in `renormalize`
  path.
- **Dead-series pruning**: an evicted count-family-split series is now removed
  from the zero-fill tracker, so `MaxSeries` eviction is no longer undone by the
  series being resurrected — and re-alerted as a drop-to-zero — every bucket.
- **Granger stability**: lead-lag causality standardizes both series before the
  OLS, so the ridge no longer crushes genuine but tiny-magnitude coefficients.
  The improvement/F statistics are scale-invariant, so well-scaled inputs are
  unchanged.
- **Restore guards**: `warmup <= 0` from a crafted snapshot falls back to the
  default instead of reaching the models with zero history; `non_zero_count` no
  longer advertises a zero-fill it never performed (empty buckets stay null,
  matching Elastic).

### Server hardening
- **Bounded forecast horizon** (reject > 10000), so a crafted `horizon` can no
  longer force a multi-GB band allocation.
- **Bounded lead/lag** — `/v1/leadlag` rejects `max_lag > 1000` and `order > 100`
  (previously only the lower bound was clamped, so a ~40-byte request could force
  a multi-GB/TB allocation via `CrossCorrelation`/`Granger` → OOM).
- **Input-length cap** (100000) on the `series` accepted by the forecast,
  changepoints and lead/lag endpoints, bounding worst-case compute.
- **Bounded `/v1/correlate`** (symptoms ≤ 50000, changes ≤ 1000) — closes an
  O(n²) symptom-grouping CPU DoS; **bounded `/v1/outliers`** `k` (≤ 200) —
  closes an `n×k` OOM and O(k²) blow-up.
- **Bounded topology orphan spans** — the trace-correlation graph now caps the
  *total* number of unresolved (orphan) spans, not just the number of keys, so a
  stream of children whose parent never arrives can no longer grow memory
  without bound (previously reachable via `/v1/otlp/v1/traces`).
- **Bounded log categories** — the Drain log-template tree caps total clusters
  (5000), so high-cardinality/random log lines can no longer create unlimited
  templates; overflow lines fold into the nearest existing template.
- **Remote-write protobuf** rejects a length-delimited field whose length prefix
  overflows `int` (bit 63 set) with a clean error instead of a slice-bounds
  panic; `/v1/incidents` now requires GET.
- **Entity caps** on live jobs, stored results, SLO series and forecasts — a
  stream of unique names can no longer grow server memory without bound; the
  snapshot-restore path enforces the same caps.
- **Request timeouts** (read/write/idle) on the serve path (slow-client
  protection), a panic-recovery middleware that returns a clean 500, a generic
  history-read error (no path leak), and a loud startup warning when the API is
  served without an auth token.

### Deploy
- The Helm chart passes `--demo=false` by default and pins its appVersion to the
  release; CI builds the Docker image on every push and pushes it to GHCR on
  version tags, so `image.tag` resolves to a published image.

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

## [0.8.0]

The remaining Elastic-ML operational-parity backlog, plus a latent bug the work
surfaced. Each item tested; all packages green.

- **Elasticsearch datafeed can now feed split detectors**: a `SplitField` adds a
  `terms` sub-aggregation, so the source yields one series per partition/by value
  (previously it collapsed everything to a single series). Long ranges are fetched
  in time chunks (≤5000 buckets/query) to respect `search.max_buckets`.
- **Model-snapshot retention**: `FileStore.RetainVersions(name, maxAge)` prunes
  snapshots older than a cutoff (always keeping the newest), the
  `model_snapshot_retention_days` equivalent.
- **Forecast lifecycle**: forecasts are now persisted artifacts — `POST/GET/DELETE
  /v1/forecasts` with per-forecast `expires_in`, one active forecast per job
  (re-POST overwrites), expired ones pruned on read.
- **Job groups**: `Job.Groups` + `GET /v1/jobs?group=<g>` filter; groups surfaced
  in live job status.
- **Bug fix**: the JSON job loader silently dropped `groups` **and**
  `sensitivity` (they were absent from the parse shadow-struct), so adaptive
  sensitivity set via a JSON job spec was never applied. Both now round-trip.

## [0.7.0]

A deep algorithmic-correctness audit of the core math, then every genuine finding
fixed with a regression test. All packages green.

### Correctness bugs fixed (were wrong on the live path)
- **Exponential distribution tail was two-sided**, so a value at the mode (x≈0 —
  the *most* typical for a right-skewed metric) scored 100. Now side-aware:
  upper-tail by default, lower-tail only when `SideLow` (outage) is requested.
- **`poissonCDF` looped O(k)** with no early exit — a single large count on a
  Poisson-fit series could stall the engine for millions of iterations. Now
  converges-and-breaks, with a normal approximation for large k.
- **One-sided distribution detectors** emitted a two-sided p (½ the sensitivity
  of the metric detector). Now one-tail.

### Statistical soundness
- **AIC family selection** compared a discrete PMF (Poisson) against continuous
  densities; for integer data the continuous candidates are now discretized
  (bin-integrated) so the comparison is measure-consistent.
- **`rare`/`freq_rare`** now emit a genuine empirical-frequency probability, so
  fusion and renormalization see an honest p (the display score keeps its useful
  scale).
- **Ensemble** combines detectors with **Fisher's method** (χ² on −2·Σln p), not
  a raw product of p-values (which grew with detector count regardless of
  evidence).
- **Granger** OLS is regularized (ridge) with a scale-relative pivot — no more
  garbage from collinear / large-magnitude series.
- **Forecast bands** widen ∝ √h (were nearly flat), so multi-step breach
  probabilities aren't over-confident.
- **Seasonality** picks the period by ACF magnitude, not smallest lag.
- **Covariance** uses the n−1 estimator.

### Determinism
- `shannonEntropy` sums in sorted-key order; `evictCommon` and
  `OrderByCausality` got stable tiebreaks — no map-order leaking into a value or
  ordering.

### Elastic-ML parity
- **`initial_score`** preserved on records and buckets across renormalization
  (Elastic's initial_record_score / initial_anomaly_score).
- **Model-memory byte estimate + `model_memory_status`** (ok/soft_limit/
  hard_limit), surfaced in live job status; optional `ModelMemoryLimit`.

## [0.6.0]

Elastic-ML parity + precision hardening, driven by an honest gap audit. Each item
has a regression test; all packages green.

### Precision / correctness
- **Per-series score renormalization**: `RenormalizeResults` now anchors severity
  per (detector, series) with the absolute full-scale floor kept, so one extreme
  series no longer crushes every other partition's normalized score.
- **Model-plot bounds for every detector kind**: distribution, population, geo
  (`lat_long`) and multivariate records now carry `lower`/`upper` (previously
  zero) — the multivariate band from a Wilson–Hilferty χ² radius.
- **Rarity-scaled `rare`/`freq_rare` score**: the score reflects how rare the
  value is across the window (−log₁₀ frequency) instead of a flat 70.

### Elastic-ML parity
- **Combined `over` + `by`/`partition`**: population analysis runs one pooled
  baseline per by/partition split, so `mean(v) over user partition region` works.
- **Delayed-data grace** (`Engine.Grace`, query-delay semantics): late points land
  in their still-open bucket and are scored once, instead of being dropped;
  `LateAccepted` counter added.
- **`summary_count_field`**: `count` can sum a pre-aggregated per-row count field.
- **`skip_model_update` rule action**: a matching bucket is still reported but not
  learned, so a known outlier can't poison the baseline.
- **Per-partition count zero-fill**: a `count by host` scores a silent host as a
  real zero (drop-to-zero anomaly), per split value.
- **Recurring calendars**: `Calendar` supports `recur_daily` / `recur_weekly`
  (maintenance every Sunday), not just one-shot windows.
- **Anomaly explanation**: records carry `multi_bucket_impact` (0..5).

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
