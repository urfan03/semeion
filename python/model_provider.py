"""semeion — optional Python model provider (research-grade heavy models).

This mirrors the Go `model.Provider` contract:

    detect_seasonality(series)     -> list[int]      dominant period(s)
    decompose(series, period)      -> dict           trend / seasonal / resid
    forecast(series, horizon)      -> list[float]
    change_points(series)          -> list[int]      indices of mean shifts

The Go engine ships a pure-Go provider by default (zero deps, single binary).
This module is the OPTIONAL plane: when you want SOTA models (STL, ETS/Prophet,
PELT/BOCPD, pyod outliers), run this as a sidecar and point the engine at it.

Status: reference implementation of the math. The gRPC service wrapper (so the
Go engine can call it over the ModelProvider contract) is the next step — the
function signatures here ARE that contract.

Deps (optional): numpy, scipy, statsmodels, ruptures.
    pip install -r requirements.txt
"""

from __future__ import annotations

from typing import List

import numpy as np


def detect_seasonality(series: List[float]) -> List[int]:
    """Dominant period(s) via the autocorrelation's first prominent peak."""
    x = np.asarray(series, dtype=float)
    n = x.size
    if n < 8:
        return []
    x = x - x.mean()
    denom = np.dot(x, x)
    if denom == 0:
        return []
    max_lag = min(n // 2, 1440)
    acf = np.array([np.dot(x[: n - lag], x[lag:]) / denom for lag in range(max_lag + 1)])
    for lag in range(2, max_lag):
        if acf[lag] >= 0.3 and acf[lag] > acf[lag - 1] and acf[lag] >= acf[lag + 1]:
            return [lag]
    return []


def decompose(series: List[float], period: int) -> dict:
    """Additive STL decomposition (falls back to a naive split if statsmodels
    is unavailable)."""
    x = np.asarray(series, dtype=float)
    try:
        from statsmodels.tsa.seasonal import STL

        res = STL(x, period=period, robust=True).fit()
        return {
            "trend": res.trend.tolist(),
            "seasonal": res.seasonal.tolist(),
            "resid": res.resid.tolist(),
        }
    except Exception:
        # Naive fallback: moving-average trend + per-phase seasonal mean.
        n = x.size
        half = period // 2
        trend = np.array(
            [x[max(0, i - half) : min(n, i + half + 1)].mean() for i in range(n)]
        )
        detr = x - trend
        phase = np.array([detr[p::period].mean() for p in range(period)])
        phase -= phase.mean()
        seasonal = np.array([phase[i % period] for i in range(n)])
        return {"trend": trend.tolist(), "seasonal": seasonal.tolist(),
                "resid": (x - trend - seasonal).tolist()}


def forecast(series: List[float], horizon: int) -> List[float]:
    """Forecast with Holt-Winters (ETS) when seasonal; else linear trend."""
    x = np.asarray(series, dtype=float)
    if x.size == 0 or horizon <= 0:
        return [0.0] * max(horizon, 0)
    periods = detect_seasonality(x)
    try:
        from statsmodels.tsa.holtwinters import ExponentialSmoothing

        if periods:
            model = ExponentialSmoothing(
                x, trend="add", seasonal="add", seasonal_periods=periods[0]
            ).fit()
        else:
            model = ExponentialSmoothing(x, trend="add").fit()
        return np.asarray(model.forecast(horizon)).tolist()
    except Exception:
        # Linear-trend fallback.
        idx = np.arange(x.size)
        a, b = np.polyfit(idx, x, 1)
        return [float(a * (x.size + h) + b) for h in range(horizon)]


def change_points(series: List[float]) -> List[int]:
    """Change-point detection via ruptures (PELT); CUSUM-ish fallback."""
    x = np.asarray(series, dtype=float)
    if x.size < 8:
        return []
    try:
        import ruptures as rpt

        algo = rpt.Pelt(model="l2").fit(x)
        # penalty scaled by variance; drop the final index rpt appends.
        bkps = algo.predict(pen=3 * float(np.var(x)) + 1e-9)
        return [b for b in bkps if b < x.size]
    except Exception:
        mean, sd = float(x.mean()), float(x.std())
        if sd == 0:
            return []
        cps, s_hi, s_lo = [], 0.0, 0.0
        k, h = 0.5 * sd, 5 * sd
        for i, v in enumerate(x):
            d = v - mean
            s_hi = max(0.0, s_hi + d - k)
            s_lo = max(0.0, s_lo - d - k)
            if s_hi > h or s_lo > h:
                cps.append(i)
                s_hi = s_lo = 0.0
        return cps


def fit_distribution(samples: List[float]) -> dict:
    """Best-fit distribution by max log-likelihood among the applicable
    candidates (normal / lognormal / exponential / poisson)."""
    x = np.asarray(samples, dtype=float)
    n = x.size
    if n < 4:
        return {"family": "normal", "params": [float(x.mean()) if n else 0.0, 1.0], "loglik": 0.0}

    mean = float(x.mean())
    sd = float(x.std())
    all_nonneg = bool(np.all(x >= 0))
    all_pos = bool(np.all(x > 0))
    all_int = bool(np.all(x == np.round(x)))

    def normal_ll(mu, s):
        if s <= 0:
            return -np.inf
        return float(np.sum(-0.5 * np.log(2 * np.pi) - np.log(s) - 0.5 * ((x - mu) / s) ** 2))

    best = {"family": "normal", "params": [mean, sd], "loglik": normal_ll(mean, sd)}

    if all_pos:
        lx = np.log(x)
        lm, lsd = float(lx.mean()), float(lx.std())
        if lsd > 0:
            ll = float(np.sum(-0.5 * np.log(2 * np.pi) - np.log(lsd) - 0.5 * ((lx - lm) / lsd) ** 2 - lx))
            if ll > best["loglik"]:
                best = {"family": "lognormal", "params": [lm, lsd], "loglik": ll}
    if all_nonneg and mean > 0:
        rate = 1.0 / mean
        ll = float(np.sum(np.log(rate) - rate * x))
        if ll > best["loglik"]:
            best = {"family": "exponential", "params": [rate], "loglik": ll}
    if all_nonneg and all_int and mean > 0:
        from math import lgamma

        ll = float(np.sum([k * np.log(mean) - mean - lgamma(k + 1) for k in x]))
        if ll > best["loglik"]:
            best = {"family": "poisson", "params": [mean], "loglik": ll}
    return best


def outliers(rows: List[List[float]], k: int = 0, features: List[str] | None = None) -> List[dict]:
    """Batch outlier detection over a table (Elastic's DFA outlier detection).

    Uses pyod's ensemble when it is installed; the Go side falls back to its own
    four-method ensemble on any error, so pyod is genuinely optional.

    Returns one dict per row, in input order:
        {"index": i, "score": 0..1, "methods": {...}, "influence": {...}}
    """
    from pyod.models.knn import KNN
    from pyod.models.lof import LOF
    from pyod.models.ecod import ECOD

    x = np.asarray(rows, dtype=float)
    n, d = x.shape
    if n < 3:
        raise ValueError("outlier detection needs at least 3 rows")

    # Robust standardization (median / MAD), matching the Go implementation.
    med = np.median(x, axis=0)
    mad = 1.4826 * np.median(np.abs(x - med), axis=0)
    scale = np.where(mad > 0, mad, np.where(x.std(axis=0) > 0, x.std(axis=0), 1.0))
    z = (x - med) / scale

    if k <= 0:
        k = min(5, n - 1)
    models = {"knn": KNN(n_neighbors=k), "lof": LOF(n_neighbors=k), "ecod": ECOD()}

    scores = {}
    for name, m in models.items():
        m.fit(z)
        raw = np.asarray(m.decision_scores_, dtype=float)
        # Same normalization as Go: robust z in log space through a logistic
        # centred at 3 deviations, so the numbers mean the same thing.
        lg = np.log(np.maximum(raw, 1e-12))
        c = np.median(lg)
        s = 1.4826 * np.median(np.abs(lg - c))
        if s <= 0:
            s = lg.std() or 1.0
        scores[name] = 1 / (1 + np.exp(-((lg - c) / s - 3)))

    ens = np.mean(np.vstack(list(scores.values())), axis=0)

    # Feature influence: each column's share of the squared distance to the k
    # nearest neighbours.
    from scipy.spatial import cKDTree

    tree = cKDTree(z)
    _, idx = tree.query(z, k=k + 1)  # the first hit is the row itself
    out = []
    for i in range(n):
        nb = [j for j in idx[i] if j != i][:k]
        per = ((z[i] - z[nb]) ** 2).sum(axis=0)
        total = per.sum()
        inf = None
        if features and total > 0:
            inf = {name: float(per[f] / total) for f, name in enumerate(features)}
        out.append({
            "index": i,
            "score": float(ens[i]),
            "methods": {name: float(v[i]) for name, v in scores.items()},
            "influence": inf,
        })
    return out


if __name__ == "__main__":
    # Tiny self-check on a synthetic seasonal series.
    demo = [100 + 30 * np.sin(2 * np.pi * i / 12) for i in range(120)]
    print("period:", detect_seasonality(demo))
    print("forecast:", [round(v, 1) for v in forecast(demo, 6)])
