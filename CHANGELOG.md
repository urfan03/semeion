# Changelog

All notable changes to semeion are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Detection-accuracy work against the TSB-AD (NeurIPS'24) finding that simple
statistical detectors beat deep networks on time-series anomaly detection.
Everything here is additive and pure Go — new packages alongside the existing
engine, whose behaviour is unchanged unless you opt in.

### Read this before the numbers: point-adjusted F1 is not usable on NAB

The corpus gate scores a **random number generator** alongside every detector.
On NAB it reaches **PA-F1 = 0.9663** — higher than any real detector here. NAB's
anomaly windows are wide enough that random noise touches every one of them, and
point adjustment then credits the whole window. Kim et al. (AAAI'22) predicted
exactly this; the gate now demonstrates it on our own corpus. VUS-PR is inflated
the same way by its existence reward (random: 0.6115).

So **PA-F1 and VUS-PR are reported but gate nothing**. The metrics that actually
separate signal from noise on this corpus, and which the gate enforces, are
range-F1 (Tatbul et al., NeurIPS'18), AUC-PR, and F1 at a **fixed** EVT-chosen
operating point rather than an oracle per-series threshold.

### Measured on NAB (58 series, 52 labelled)

| detector          | AUC-PR | range-F1 | PA%20-F1 | fixed-F1 | *PA-F1* |
|-------------------|--------|----------|----------|----------|---------|
| random scorer     | 0.1022 | 0.2675   | 0.5141   | 0.1541   | *0.9663* |
| existing engine   | 0.1773 | 0.3477   | 0.5387   | 0.0831   | *0.7366* |
| `mp`              | 0.1112 | 0.3448   | 0.4870   | 0.3413   | *0.9432* |
| `mp` DAMP         | 0.1108 | 0.3476   | 0.4954   | 0.3123   | *0.9480* |
| `sub` KNN         | 0.1264 | 0.3969   | 0.4900   | 0.2726   | *0.9045* |
| `sub` LOF         | 0.1142 | 0.3651   | 0.4902   | 0.3581   | *0.9219* |
| `sub` PCA         | 0.1131 | 0.3305   | 0.4917   | 0.2276   | *0.9161* |
| `sub` IForest     | 0.1179 | 0.3657   | 0.4891   | 0.3207   | *0.9249* |
| `hst`             | 0.1934 | **0.5217** | 0.5840 | **0.5537** | *0.9355* |
| `evt` DSPOT       | 0.1973 | 0.4489   | 0.6252   | 0.3354   | *0.9540* |
| `selector`        | 0.1960 | 0.4789   | 0.6020   | 0.3958   | *0.9556* |
| **Fisher(evt,mp,hst)** | **0.2027** | 0.5214 | **0.6443** | 0.4829 | *0.9662* |

Honest reading of that table: only `hst`, `evt`, the Fisher ensemble and the
selector beat the existing engine on AUC-PR. The matrix-profile and subsequence
detectors land *below* the engine there — they are near-random at ranking the
whole series, and only their extreme tail carries signal, which is why they
still beat it by 3-4× at a fixed operating point and why they earn their place
as ensemble inputs rather than as standalone replacements. The gate encodes that
split: `hst`, `evt`, `fisher`, `fisher-all` and `selector` must beat the engine
on both AUC-PR and range-F1; the rest must beat random on both and beat the
engine at a fixed threshold.

### Chasing 80% precision: what the audit found

Auditing the false alarms answered the question before any more tuning could.
`semeion nab-corpus --audit` dumps the strongest alarms that fell outside a
labelled window, with their shape, magnitude and surrounding context. Of 60
audited on NAB:

- **36 have |z| ≥ 20.** One is `cpu_utilization_asg_misconfiguration` going from
  a 31% baseline to **100% CPU**. That is an anomaly by any definition; NAB
  simply does not label it.
- **All 60 sit more than 20 samples from any labelled window**, so these are not
  boundary-alignment artefacts — they are unlabelled events.
- **50 of 60 are single-sample spikes.** NAB deliberately labels sustained
  anomalies and ignores momentary ones.

So measured precision on NAB **understates** real precision, and 80% is not
reachable against this ground truth: a large share of the remaining "errors" are
correct detections of things the benchmark chose not to mark. The audit tool
exists so the same question can be answered against your own incident history,
where the labels are the ones you actually care about.

### What raised precision, measured

| configuration | recall | precision | alarms/series |
|---------------|--------|-----------|---------------|
| EVT q=1e-3 (reference) | 0.5603 | 0.5139 | 6.21 |
| **alarm budget 1/2000 + effect gate** | 0.4483 | **0.6419** | **2.85** |
| duration ≥ 20 (shape gate) | 0.1552 | 0.7027 | 17.08 |
| duration ≥ 20 + refractory | 0.1552 | 0.6552 | 0.56 |
| EVT q=1e-3 + `Precise` policy | 0.2069 | 0.7238 | 8.08 |

The budget-plus-effect point is the one to ship: **+13 points of precision over
the reference while keeping 45% recall and less than half the alarm volume.**
Requiring a minimum duration trades precision for recall monotonically
(0.4670 at ≥3 samples → 0.7027 at ≥20), which is a dial, not a win.

### False-discovery-rate control: diagnosed and fixed

FDR control is the textbook route to a precision target — precision = 1 − FDR, so
controlling FDR at 0.2 should hand you 80% precision by construction. The first
attempt failed badly: at a nominal q=0.05, which should give ~95% precision,
measured precision was **0.2781**.

The guarantee needs valid p-values. Three separate things were invalidating ours,
and each has been fixed and independently verified against a *known* null:

1. **Detector dependence.** The three detectors all read the same series, so
   Fisher and the order-statistic combiner — both of which assume independence —
   break. Replaced with the **Cauchy combination test** (`fuse.Cauchy`), whose
   tail is valid under arbitrary dependence. Under *perfectly* dependent inputs
   at a nominal 5%: **Cauchy 0.0493, Fisher 0.1594, order-statistic 0.1893.**
2. **Autocorrelation and window scanning.** Taking the maximum over a run of
   correlated samples and comparing it to a pointwise null is a scan statistic
   judged against the wrong distribution. Added
   **`conformal.NewBlock`**, which calibrates the distribution of the sliding
   maximum at each run length. On an AR(1) series with ρ=0.9, a 16-point scan at
   nominal α=0.01: **block calibration 0.0062, pointwise 0.0414.**
3. **Contaminated null.** The empirical null was estimated from data containing
   the anomalies being detected. Added trimmed variants
   (`fuse.TrimmedPValues`, `conformal.NewTrimmed`) plus a **two-stage
   split-conformal** construction — the reference period is halved, one half
   calibrating the per-detector p-values and the other calibrating the combined
   scan statistic, so nothing is calibrated against itself.

A fourth cause turned out to be **distribution shift**: a fixed reference window
cannot certify a distant future window on a drifting series, which violates
exchangeability itself rather than any particular estimator. Recalibrating on a
**sliding** reference window fixes what can be fixed there — at a matched target
level it raises event recall from **0.4224 to 0.6293** with the same precision.

And the measured FDR on NAB stays around 0.88 no matter which of these is
applied — because that figure is **not an FDR measurement**. At the candidate
threshold the pipeline proposes 4229 regions across 58 series while NAB labels
116 windows, so the label base rate caps event precision at **0.0274** however
valid the p-values are. Measured precision is 0.1201, **4.4× the base rate**, and
the audit above shows most of the unlabelled candidates are real anomalies NAB
did not mark. NAB cannot validate an FDR guarantee; the unit tests, where the
null is known, can and do.

### Precision work: from one operating point to a frontier

The plain ensemble caught 37% of anomaly windows with 49% alarm precision — one
false page for every real one. Six additions move that from a single point to a
frontier you can choose from. Measured event-level on NAB, all at the same
EVT threshold (q=1e-3) with no alarm policy, so each row is the stage's own
contribution:

| stack | event recall | alarm precision | event F1 |
|-------|--------------|-----------------|----------|
| Fisher over raw values      | 0.3707 | 0.4905 | 0.4223 |
| + deseasonalized input      | 0.4224 | 0.5000 | 0.4579 |
| + calibrated agreement k=2  | 0.5603 | 0.5139 | **0.5361** |
| + multi-scale DAMP          | 0.6207 | 0.4780 | 0.5401 |

Layering the alarm policy on top gives 126 configurations, 13 of them on the
Pareto frontier. The ones worth knowing:

| what you want | configuration | recall | precision | alarms/series |
|---------------|---------------|--------|-----------|---------------|
| never page falsely | multi-scale k=3, q=1e-4, `Precise` | 0.0345 | **1.0000** | 0.6 |
| very high precision | Fisher, q=1e-4, no policy | 0.1207 | 0.7895 | 0.7 |
| high precision, usable recall | agreement k=2, q=1e-3, `Precise` | 0.2069 | 0.7238 | 8.1 |
| best balance | multi-scale, q=1e-3, no policy | 0.6207 | 0.4780 | 6.6 |
| catch almost everything | deseasonalized, q=1e-2, no policy | 0.8190 | 0.4141 | 74.1 |

So precision at a *fixed* recall barely moved — the detectors were already near
what NAB's labels support — but the reachable frontier widened enormously: 100%
alarm precision is now an available setting, and 82% recall is too.

Two things did not pay off, recorded so nobody re-tries them blind. **Conformal
scoring never reached the frontier** on NAB: its p-values quantise at
1/(n_cal+1), and stacking a distribution-free layer on an already-calibrated
agreement score only coarsens it. Its value is the *guarantee* — a provable
false-alarm rate — not a better NAB number, and on a corpus that labels whole
windows the guarantee buys nothing. **Requiring all detectors to agree (k=3)
is worse than a majority (k=2)** at every threshold: 0.4415 vs 0.5361 event F1.

### What that means as an operating point

Threshold metrics still hide the thing an operator asks for, so the runner also
reports event recall and alarm precision. At the EVT-chosen threshold
(q=1e-3) over the 52 labelled series (~365k points, 116 anomaly windows):

| detector | anomaly windows caught | alarms raised | alarm precision |
|----------|------------------------|---------------|-----------------|
| `hst`    | **55 of 116 (47.4%)**  | 297           | **52.5%**       |
| Fisher   | 43 of 116 (37.1%)      | 316           | 49.1%           |
| `evt`    | 29 of 116 (25.0%)      | 308           | 49.7%           |

That is the number to quote: roughly **half the anomaly windows caught, and
roughly every second alarm real**, at a threshold nothing tuned against the
labels. Loosening `q` trades one for the other.

Raw point-wise F1 at the same threshold is ~0.01 for every detector, and that
figure is meaningless here: NAB marks ~33,500 individual points as anomalous
because it labels whole windows, while a detector is supposed to fire once per
event. The runner reports it (`fixed_raw_f1`) only so nobody has to re-derive
why it looks catastrophic.

Two negative results worth recording. Online reliability weighting
(`fuse.WeightedCombine`) *loses* to unweighted Fisher over the same eight
detectors on NAB (AUC-PR 0.1592 vs 0.1789) — it works when one detector is
clearly broken, which is what its unit tests cover, but here all eight are
mediocre and correlated, so down-weighting throws away signal. And widening the
ensemble from three detectors to eight also hurts (0.2027 → 0.1789): the six
weak members outvote the two strong ones.

### New detectors
- **Half-space trees** (`hst`): the streaming mass-based ensemble from Tan et
  al., matching River's variant — random-midpoint trees over a randomized work
  range, reference/latest mass windows swapped every `WindowSize` points, score
  summed along the path until a node falls under the size limit. Scores are
  causal (zero until the first window closes) and reproducible for a given seed.
  Ships with an online min-max `Scaler` and a `Series` helper that lag-embeds a
  univariate series.
- **Extreme-value thresholding** (`evt`): POT with a Grimshaw profile-likelihood
  GPD fit (grid + golden-section over θ, exponential limit included), plus
  streaming `SPOT` and drift-aware `DSPOT`. `StreamProbabilities` returns a
  calibrated tail p-value per point — the GPD survival function above the
  initial threshold, the empirical right tail below it — so a detector's raw
  score becomes something comparable across detectors. Peak storage is bounded
  (`MaxPeaks`) while the exceedance rate stays unbiased.
- **Fisher combination** (`fuse`): `Fisher`, a causal empirical-tail p-value
  estimator, and `FisherStreams` for combining several detectors point by point.
  Feeding EVT's *native* p-values into the combination beats converting them
  empirically first (0.9662 vs 0.9570 macro PA-F1).

### Matrix profile
- `Scores` wraps the STOMP self-join with the settings that measured best:
  per-subsequence output (spreading a discord over its whole window costs ~0.17
  PA-F1 on NAB) and a small default window.
- Fixed constant-window handling. `meanStd`'s rolling sums drift, so an
  all-constant stretch produced σ = 0 at some offsets and σ ≈ 6e-8 at others —
  the same flat data scored either a maximal discord or a perfect match
  depending on position. Windows whose σ falls under a scale-relative epsilon
  now match each other and only mismatch against non-flat windows. Worth
  +0.03…+0.04 PA-F1 at every window size tested. `MatrixProfile` keeps the
  textbook behaviour; the fix is on by default in `Scores`.

### More detectors
- **Subsequence detectors** (`sub`): a shared sliding-window embedding with
  optional z-normalisation and trivial-match exclusion, feeding `KNN`, `LOF`,
  `PCA` (reconstruction error over a Jacobi eigendecomposition, keeping enough
  components for a target variance) and `IForest`. `Population` routes the same
  embedding through the existing `outlier` package so the knn/kth-nn/lof/ldof
  ensemble and its influence attribution work on subsequences too.
- **DAMP** (`mp.DAMP`): causal discord discovery — backward doubling with early
  abandoning over a pruning vector, on top of a new FFT-based `MASS` distance
  profile. Scores each point against earlier subsequences only and is 5× faster
  than `LeftMatrixProfile` at n=4000, m=16 (18ms vs 92ms), with the gap widening
  as the window grows. `Lookahead` defaults to 0 to keep it strictly causal.

### Ensembling
- **Stouffer's method with weights** (`fuse.Stouffer`) on an Acklam inverse
  normal, plus `Reliability` — online per-detector weights from a decayed
  Youden's J against a **leave-one-out** consensus, scaled by how far each
  detector's firing rate sits above its nominal level. The leave-one-out part
  matters: judging a detector against a consensus it helped form lets one noisy
  detector capture the vote and weight itself up.
- **Per-series detector selection** (`selector`): nine shape features
  (length, autocorrelation peak and its lag, trend, noise, skew, kurtosis,
  spikiness, flatness) feeding a normalised k-NN vote over labelled examples,
  with `Without` for leave-one-out evaluation and JSON round-tripping so a
  model trained on your own corpus can be shipped. On NAB it picks `evt` for 28
  series and `hst` for 21.

### Benchmark
- **Metrics** (`benchmark`): point-adjusted scoring (`Segments`, `PointAdjust`,
  `PointAdjustedScore`, `BestPointAdjustedF1`, `BestF1`), `AUCPR` (tie-aware
  average precision), `PointAdjustedAUCPR`, **PA%K** (`PointAdjustK`,
  `BestPointAdjustedKF1`), **range-based precision/recall** with flat / front /
  back / middle positional bias (`RangeRecall`, `RangePrecision`, `RangeF1`,
  `BestRangeF1`), and **VUS-ROC / VUS-PR** (`RangeAUC`, `VUS`) over a
  cosine-decayed label buffer with the existence reward.
- **Corpus runners**: `LoadCorpusRoot` reads a NAB checkout from either layout
  (`labels/combined_windows.json` or the root), and `LoadUCRCorpus` reads the
  **UCR Anomaly Archive** — the benchmark NAB's critics recommend — parsing the
  anomaly range straight out of each filename. `RunCorpusWith` adds every metric
  above plus a **fixed-threshold protocol**: pass a `ThresholdFunc` (EVT-chosen,
  or `QuantileThreshold`) and get F1 at a real operating point instead of an
  oracle sweep. Unlabelled series are skipped explicitly, never counted as
  perfect.
- **Event-level scoring** (`Operating`, `EventScore`, `Curve`): anomaly windows
  caught and alarm precision, which is what an operator actually asks for, plus
  alarms per series so alarm volume is visible. `RunCorpusWith` also reports raw
  point-wise F1 at the fixed threshold (`fixed_raw_f1`) — ~0.01 for everything,
  and meaningless here, since NAB marks ~33,500 points anomalous by labelling
  whole windows while a detector fires once per event. It is reported so nobody
  has to re-derive why it looks catastrophic.
- `semeion nab-corpus --dir D` (or `--ucr D`) runs any detector or ensemble from
  the CLI, and `--policy precise --q 1e-3` adds the event-level report at a named
  operating point. `make gate-nab` / `make gate-ucr` run `TestNABCorpusGate`;
  `TestPrecisionStack` walks the whole stack and prints the Pareto frontier. All
  skip unless `SEMEION_NAB_DIR` / `SEMEION_UCR_DIR` is set.

### The shipped detector

Everything above is a library of parts. **`pipeline`** is the one entry point
that assembles them the way the measurements say they should be assembled, so a
caller does not have to re-derive it — and cannot accidentally reach for a batch
detector on a live stream.

- **Causal by construction.** DAMP, half-space trees and DSPOT only; the batch
  self-join is not reachable from here.
- **Bounded.** A rolling `History` window with a hard cap, validated at
  construction. `Push` re-scores every `Refresh` points rather than every point,
  which took 4000 pushes from 139s to 4.5s — the difference between a demo and
  something that can sit in front of a metric.
- **Late-confirming and honest about it.** `Push` returns alarms this point newly
  *confirms*, with absolute indices; a persistence policy cannot rule on a point
  until it has seen what follows, so an alarm's start may sit behind the point
  just pushed. Each alarm is reported once.
- **Points at the region, not the edge.** A level shift makes both of its edges
  look like changes, so a detector can fire on the recovery, where the value is
  already back to normal. Alarms anchor on the largest deviation near the
  crossing and grow while the deviation stays comparable, so the reported window
  is the elevated stretch and the effect size means something.
- **Four named sensitivities** plus optional duration, effect-size, budget and
  deseasonalizing gates.

Measured end to end on NAB, counting **pages** — one contiguous alarm region is
one page, which is what an operator actually receives:

| configuration | recall | precision | F1 | pages/series |
|---------------|--------|-----------|----|--------------|
| `Sensitive` | 0.3103 | 0.3736 | 0.3391 | 1.75 |
| `Balanced` | 0.3017 | 0.3929 | 0.3413 | 1.62 |
| `Precise` | 0.2069 | 0.6452 | 0.3133 | 0.60 |
| **`Balanced` + duration ≥ 5** | 0.0776 | **0.8182** | 0.1417 | **0.21** |
| **`Balanced` + budget 1/2000** | **0.4741** | 0.4186 | **0.4446** | 2.48 |
| `Balanced` + budget + effect | 0.3362 | 0.4810 | 0.3958 | 1.52 |

The duration-gated setting clears **82% precision** at a fifth of a page per
series. Against NAB's labels, which the audit shows are missing many real
anomalies, that is a floor rather than a ceiling.

### Ranking alarms once feedback exists

**`rank`** is the last piece of the plan and the one that cannot be validated
here. It extracts nine features that the pipeline already computes — score,
effect size, duration, detector agreement, shape persistence, seasonality,
noise, change proximity, peer corroboration — and fits a logistic model with
**monotonicity constraints**: a weight that would learn "a bigger effect means
less likely real" is clamped to zero rather than trusted, because that is
overfitting, not a finding. `Threshold` calibrates a cut against a false-rate
budget and errors rather than silently returning a useless one when the budget
is unreachable. `Learn` is online, so `MarkFalsePositive` can drive it directly.

It is unit-tested on synthetic separable data and **not** measured on NAB,
deliberately: fitting it to a benchmark whose labels we have shown to be
incomplete would produce a number that means nothing. It needs a few hundred
operator verdicts on your own alarms.

### New packages for the precision work
- **`fdr`**: Benjamini-Hochberg, Storey-adaptive BH, Benjamini-Yekutieli for
  arbitrary dependence, and online **LORD++** with a decaying alpha-wealth
  sequence. Tested against the step-up rule on random inputs, for FDR control
  and power on synthetic mixtures, and for near-silence under a pure null.
- **`fuse.Cauchy` / `HarmonicMean`**: dependence-robust p-value combination, the
  fix for detector dependence described above. `CauchyStreams` applies it
  point-by-point across detector streams.
- **`conformal.NewBlock`**: scan-statistic calibration. Calibrates the sliding
  maximum at powers-of-two run lengths and picks the right one per candidate, so
  a run of correlated samples is judged against the distribution of runs rather
  than of points.
- **`peer`**: cross-series evidence, the lever that actually reaches past a
  single metric. `Relative` divides each series by the cross-sectional median of
  its peer group and then takes a causal robust z of that ratio, so a fleet-wide
  traffic surge leaves every ratio unchanged while a single-instance fault stands
  out — verified on a synthetic fleet where the solo view fires on both and the
  peer view only on the fault. `Deviation` is the cross-sectional primitive,
  `Normalize` the per-series causal robust z, and `Corroborate` builds
  corroborating p-value streams from related metrics with a Šidák correction for
  the search window and a causal mode that refuses to look forward.
  *Not validated on NAB* — its 58 series are unrelated, so co-movement cannot be
  measured there. It needs real multivariate metrics.
- **`shape`**: classifies a flagged region as spike, dip, level shift up or down,
  variance change, trend break or gap, from the baseline before, the values
  during and the recovery after. Distinguishing transient from persistent is what
  lets a duration filter be principled rather than arbitrary.
- **`guard` additions**: `Candidates` groups over-threshold points into candidate
  regions with gap bridging, which is the unit an operator reasons about and the
  right unit for a two-stage pipeline; `SolveThreshold`/`WithBudget` invert an
  alarm allowance ("at most 1 per 2000 samples") into the threshold that spends
  it, under whatever policy is active; `GateByEffect` with `RollingBaseline`
  requires a deviation to be *large* as well as significant.
- **`prep`/`conformal`/`fuse` additions**: trimmed-null variants
  (`TrimmedPValues`, `NewTrimmed`) that drop the top of the calibration sample so
  the anomalies being detected stop inflating the null they are measured against.
  On NAB this recovered a large amount of recall (0.16 → 0.39 at a fixed FDR
  level), confirming the contamination was real.

### Turning scores into alarms
- **`guard`**: the alarm policy layer, streaming and batch. K-of-N persistence
  (fire only when K of the last N points clear the bar — the biggest single
  false-alarm filter), a refractory period so one event is one alarm, warm-up
  gating so nothing fires before the detector is calibrated, index-keyed
  suppression windows for known change events, a cooldown that demands
  escalation after firing, and a feedback penalty that raises the bar for a
  series an operator marked as a false positive. Four presets — `Sensitive`,
  `Balanced`, `Precise`, `Paranoid` — name the points on the frontier above.
  Suppressed points still feed the persistence window, so a window boundary
  cannot leak a stale hit into the first post-suppression point.
- **`prep`**: autocorrelation period detection plus a causal, outlier-resistant
  deseasonalizer — the residual against a rolling **median** of the last N
  observations in the same seasonal slot. Median rather than mean, and causal
  rather than STL, both deliberately: the existing `model.Decompose` absorbed an
  injected +25 spike almost entirely into the seasonal component (assigning slot
  20 a seasonal of +15.3 where the truth was −8.7), which is fine for
  forecasting and useless for anomaly detection. `Options.STL` still routes
  through it when you want the full decomposition.
- **`fuse.Agree`**: calibrated k-of-m agreement. Instead of a hard AND, it takes
  the k-th smallest p-value and maps it through its null distribution,
  Beta(k, m−k+1), so "at least 2 of 3 detectors fired this hard" comes back as a
  proper p-value. Verified uniform under the null at k=1,2,3,5. `MultiScale` and
  `MultiScaleAgree` apply the same combiner across window sizes.
- **`conformal`**: split (inductive) conformal with a distribution-free
  guarantee — P(alarm | normal) ≤ α, verified empirically at α=0.1, 0.05, 0.01.
  `MinCalibration` says how much calibration data an α needs to be attainable at
  all, `Guarantee` reports the exact bound, and `Threshold` reproduces the alarm
  rule as a scalar. `Mondrian` conditions calibration on the seasonal slot, so a
  daily peak stops being an anomaly without losing a genuine spike on top of
  that peak.

### Production wiring
- **EVT auto-threshold in the engine**, opt-in via `job.auto_threshold`. A SPOT
  model calibrates on the raw per-bucket score distribution and then sets the
  admission threshold from the data instead of the fixed `score >= 50`. Off by
  default and byte-identical to the old behaviour when unset; bounded by
  `min`/`max`; survives restart through the engine snapshot. The threshold only
  changes at bucket boundaries, and each bucket is judged by the threshold
  learned from the buckets before it. Every score-vs-threshold comparison in the
  engine now routes through one `admits` helper, which is where the raw
  distribution is observed.
- **Feedback and calendars compose with the learned threshold.** No new
  machinery was needed: `MarkFalsePositive` already gates emission per
  (detector, series) and `Calendars` already silence a bucket, so with
  `auto_threshold` on, the learned global threshold, the per-series
  false-positive penalty and the suppression windows stack. Tests pin the
  interaction — a report drops exactly the records between the learned threshold
  and the penalised bar, and nothing else.
- **Streaming state snapshots** for `hst.Forest`, `evt.SPOT` and `evt.DSPOT`
  (`Snapshot` / `Restore*`), tested by splitting a stream in half and checking
  the resumed half is identical to the unsplit run — without these the new
  detectors could not survive a restart in a live job.
- **Multivariate half-space trees** via `hst.SeriesMulti`, so the forest's
  existing multi-dimensional support is reachable from a row feed.

### Performance
- **Parallel matrix profile** (`mp.Parallel`): a SCAMP-style diagonal
  decomposition replaces the sequential STOMP row scan, giving each worker a
  private profile that is merged at the end. Bit-comparable to `stomp` and **9×
  faster** (111ms → 12ms at n=4000, m=8 on 16 threads). `Scores` uses it by
  default; `Options.Serial` keeps the old path.
- `evt.Options.RefitEvery` throttles GPD refits, and the Grimshaw candidate grid
  shrank from ~580 evaluations to ~136 with no measurable loss of fit accuracy
  (the parameter-recovery tests are unchanged).

## [0.10.0]

Closing the last capability gaps against Elastic ML that are addressable in
code. Each feature is tested; all packages green, comment-free.

### Seasonality/trend maturity — STL
- **Real STL decomposition** (Seasonal-Trend decomposition using Loess): locally
  weighted regression on the cycle-subseries and trend, a low-pass filter, and
  bisquare robustness iterations, replacing the naive phase-average. `Decompose`
  (and therefore the forecaster) now tracks trend through the series ends far
  better and separates seasonality from a moving trend. Falls back to the
  classical decomposition for series shorter than two periods.

### Datafeed aggregation pushdown
- **Elasticsearch composite aggregation** with `after_key` pagination for split
  detectors, so a high-cardinality `by`/`partition` split yields *every* series
  instead of being truncated at the `terms` size cap. Added a `Frequency` field
  and confirmed cross-cluster (`cluster:index`) works through the same path.

### Reusable filter lists
- **Named, server-side filter lists** (`/v1/filters`, `GET`/`PUT`/`DELETE`),
  referenced by many jobs. A rule gains a `scope` (`field` + `filter_id` +
  `include`); the server resolves the referenced list and the engine applies the
  rule only to in-scope records (Elastic filter-list + rule-scope parity). Lists
  survive restart via the state snapshot.

### Horizontal scale
- **Cluster mode**: live jobs are sharded across nodes by a consistent-hash ring
  (`--self`, `--peers`), and a node transparently forwards a job-scoped request
  (create / points / status / results / history / influencers) to the node that
  owns the job, so replicas share load and memory and any node can receive any
  request. New `/v1/cluster` endpoint; a Helm `HorizontalPodAutoscaler` (CPU) and
  autoscaling values. Stateless endpoints are served wherever they land. The
  forwarding client has a 30s timeout so a hung peer can't pile up goroutines,
  and restored filter lists are re-normalized under the same size caps.

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
