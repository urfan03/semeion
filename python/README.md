# semeion — Python model plane (optional)

The Go engine ships a **pure-Go** heavy-model provider (`model.GoProvider`):
seasonality discovery, additive decomposition, seasonal-naïve forecasting and
mean-shift change points. Zero dependencies, always in the single binary. **You
do not need Python to run semeion.**

This directory is the **optional research-grade plane**. When you want
state-of-the-art models — STL, ETS/Holt-Winters/Prophet, PELT/BOCPD change
points, `pyod` outlier ensembles — run these implementations as a sidecar and
point the engine at them. They implement the same contract as the Go provider:

| Contract (`model.Provider`) | Python (`model_provider.py`) |
|---|---|
| `DetectSeasonality(series) []int` | `detect_seasonality(series)` |
| `Decompose(series, period)`       | `decompose(series, period)` — statsmodels STL |
| `Forecast(series, horizon)`       | `forecast(series, horizon)` — ETS / Holt-Winters |
| `ChangePoints(series) []int`      | `change_points(series)` — ruptures PELT |

Each function degrades gracefully to a dependency-free fallback if the heavy
library isn't installed, so the module is importable anywhere.

## Run the self-check

```sh
pip install -r requirements.txt
python model_provider.py
```

## Design

- The engine calls the provider **periodically and in batch** (re-detect
  seasonality every N buckets, forecast on demand) — never per data point — so
  the sidecar's latency never touches the streaming hot path.
- The provider is **stateless**: the engine owns all state and sends the window
  to analyse. That keeps the sidecar horizontally scalable and restart-safe.

## Status / next step

The function signatures here **are** the `ModelProvider` contract. The remaining
work is the transport wrapper — a small gRPC service exposing these four methods
(`protobuf` schema + a `model.GRPCProvider` on the Go side that dials it). Until
then, the Go provider is the live default and this module is a reference
implementation you can iterate on.
