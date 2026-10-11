"""Vectorised indicators over [T, N] hourly arrays (float32; NaN-safe via pandas rolling)."""
import numpy as np
import pandas as pd


def df(x):
    return pd.DataFrame(x)


def ema(x, n):
    return df(x).ewm(span=n, adjust=False, min_periods=n).mean().to_numpy(np.float32)


def rma(x, n):
    return df(x).ewm(alpha=1 / n, adjust=False, min_periods=n).mean().to_numpy(np.float32)


def rmax(x, n):
    return df(x).rolling(n, min_periods=n).max().to_numpy(np.float32)


def rmin(x, n):
    return df(x).rolling(n, min_periods=n).min().to_numpy(np.float32)


def rsum(x, n, minp=None):
    return df(x).rolling(n, min_periods=minp or n).sum().to_numpy(np.float32)


def rmean(x, n, minp=None):
    return df(x).rolling(n, min_periods=minp or n).mean().to_numpy(np.float32)


def rstd(x, n, minp=None):
    return df(x).rolling(n, min_periods=minp or n).std().to_numpy(np.float32)


def shift(x, k):
    out = np.full_like(x, np.nan)
    if k > 0:
        out[k:] = x[:-k]
    elif k < 0:
        out[:k] = x[-k:]
    else:
        out[:] = x
    return out


def tr(h, l, c):
    pc = shift(c, 1)
    return np.fmax(h - l, np.fmax(np.abs(h - pc), np.abs(l - pc)))


def atr(h, l, c, n=14):
    return rma(tr(h, l, c), n)


def agg(P, k):
    """Bars of k hours ending at each hour (rolling): high, low over the last k hours, close now."""
    return rmax(P["h"], k), rmin(P["l"], k), P["c"]


def atr_tf(P, k, n=14):
    """ATR(n) of k-hour bars, approximated on the hourly grid: TR of rolling k-hour bars every k hours,
    smoothed over n of them (span n×k hours)."""
    hk, lk = rmax(P["h"], k), rmin(P["l"], k)
    pc = shift(P["c"], k)
    trk = np.fmax(hk - lk, np.fmax(np.abs(hk - pc), np.abs(lk - pc)))
    return rmean(trk, n * k, minp=n * k // 2)


def ret(c, k):
    return c / shift(c, k) - 1


def logret(c, k=1):
    return np.log(c / shift(c, k))


def breadth(c, U, n):
    """Fraction of universe members with close above their EMA(n)."""
    e = ema(c, n)
    above = (c > e) & U & np.isfinite(e)
    cnt = (U & np.isfinite(e)).sum(1)
    return np.where(cnt > 0, above.sum(1) / np.maximum(cnt, 1), np.nan).astype(np.float32)


def xs_mean(x, U):
    m = np.where(U & np.isfinite(x), x, np.nan)
    with np.errstate(all="ignore"):
        return np.nanmean(m, 1)


def xs_rank(x, U):
    """Cross-sectional percentile rank in [0,1] among universe members (NaN outside)."""
    m = np.where(U & np.isfinite(x), x, np.nan)
    r = df(m).rank(axis=1, pct=True).to_numpy(np.float32)
    return r


def beta_resid(c, cb, n=168):
    """Rolling beta of hourly log returns to BTC and the residual return accumulated over windows."""
    r = logret(c)
    rb = logret(cb[:, None].repeat(1, 0))[:, 0] if cb.ndim == 1 else logret(cb)
    rb2 = np.broadcast_to(rb[:, None], r.shape)
    cov = rmean(r * rb2, n, n // 2) - rmean(r, n, n // 2) * rmean(rb2, n, n // 2)
    var = rmean(rb2 ** 2, n, n // 2) - rmean(rb2, n, n // 2) ** 2
    beta = cov / np.where(var > 0, var, np.nan)
    resid = r - np.nan_to_num(beta) * rb2
    return beta.astype(np.float32), resid.astype(np.float32)
