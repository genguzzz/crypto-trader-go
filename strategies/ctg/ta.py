"""Pine-like indicators on numpy arrays.

Every function takes array-likes (oldest first), returns float arrays of the same length with NaN
where the value is undefined (warm-up, or a NaN inside the window), and never raises on NaN input.
EMA/RMA are seeded with the SMA of the first `n` values, like TradingView.
"""

from __future__ import annotations

import numpy as np
from numpy.lib.stride_tricks import sliding_window_view

__all__ = ["sma", "ema", "rma", "rsi", "atr", "tr", "macd", "bbands", "bb", "crossover", "crossunder", "cross",
           "highest", "lowest", "stdev", "change"]


def _arr(x) -> np.ndarray:
    return np.asarray(x, dtype=float).reshape(-1)


def _window(x, n: int, fn) -> np.ndarray:
    x = _arr(x)
    n = int(n)
    out = np.full(x.shape, np.nan)
    if n < 1 or len(x) < n:
        return out
    out[n - 1:] = fn(sliding_window_view(x, n), axis=1)
    return out


def sma(x, n: int) -> np.ndarray:
    """Simple moving average."""
    return _window(x, n, np.mean)


def stdev(x, n: int) -> np.ndarray:
    """Rolling population standard deviation (Pine's biased default)."""
    return _window(x, n, np.std)


def highest(x, n: int) -> np.ndarray:
    """Rolling maximum over n values."""
    return _window(x, n, np.max)


def lowest(x, n: int) -> np.ndarray:
    """Rolling minimum over n values."""
    return _window(x, n, np.min)


def _smooth(x, n: int, alpha: float) -> np.ndarray:
    x = _arr(x)
    n = int(n)
    out = np.full(x.shape, np.nan)
    if n < 1:
        return out
    # Seed at the first window of n consecutive valid values.
    run = 0
    start = -1
    for i, v in enumerate(x):
        run = run + 1 if not np.isnan(v) else 0
        if run == n:
            start = i
            break
    if start < 0:
        return out
    prev = float(np.mean(x[start - n + 1:start + 1]))
    out[start] = prev
    for i in range(start + 1, len(x)):
        v = x[i]
        if np.isnan(v):
            continue  # a gap yields NaN here and leaves the state untouched
        prev = alpha * v + (1 - alpha) * prev
        out[i] = prev
    return out


def ema(x, n: int) -> np.ndarray:
    """Exponential moving average, alpha = 2/(n+1)."""
    return _smooth(x, n, 2.0 / (int(n) + 1))


def rma(x, n: int) -> np.ndarray:
    """Wilder's moving average, alpha = 1/n."""
    return _smooth(x, n, 1.0 / max(int(n), 1))


def change(x, n: int = 1) -> np.ndarray:
    """x - x[n bars ago]."""
    x = _arr(x)
    out = np.full(x.shape, np.nan)
    n = int(n)
    if 0 < n < len(x):
        out[n:] = x[n:] - x[:-n]
    return out


def rsi(x, n: int = 14) -> np.ndarray:
    """Relative strength index (Wilder)."""
    d = change(x, 1)
    with np.errstate(invalid="ignore"):
        up = rma(np.where(np.isnan(d), np.nan, np.maximum(d, 0)), n)
        down = rma(np.where(np.isnan(d), np.nan, np.maximum(-d, 0)), n)
        out = np.where(down == 0, 100.0, np.where(up == 0, 0.0, 100 - 100 / (1 + up / np.where(down == 0, 1, down))))
    out[np.isnan(up) | np.isnan(down)] = np.nan
    return out


def tr(high, low, close) -> np.ndarray:
    """True range; the first bar uses high - low."""
    h, l, c = _arr(high), _arr(low), _arr(close)
    prev = np.concatenate(([np.nan], c[:-1]))
    hl = h - l
    with np.errstate(invalid="ignore"):
        out = np.fmax(hl, np.fmax(np.abs(h - prev), np.abs(l - prev)))
    out[np.isnan(hl)] = np.nan
    if len(out):
        out[0] = hl[0]
    return out


def atr(high, low, close, n: int = 14) -> np.ndarray:
    """Average true range (RMA of true range)."""
    return rma(tr(high, low, close), n)


def macd(x, fast: int = 12, slow: int = 26, signal: int = 9):
    """Returns (macd, signal, histogram)."""
    line = ema(x, fast) - ema(x, slow)
    sig = ema(line, signal)
    return line, sig, line - sig


def bbands(x, n: int = 20, mult: float = 2.0):
    """Bollinger bands, Pine order: (middle, upper, lower)."""
    mid = sma(x, n)
    dev = mult * stdev(x, n)
    return mid, mid + dev, mid - dev


bb = bbands


def _pair(a, b):
    a = _arr(a)
    b = np.full(a.shape, float(b)) if np.ndim(b) == 0 else _arr(b)
    return a, b


def crossover(a, b) -> np.ndarray:
    """True where a crosses above b (b may be a scalar)."""
    a, b = _pair(a, b)
    out = np.zeros(a.shape, dtype=bool)
    if len(a) > 1:
        with np.errstate(invalid="ignore"):
            out[1:] = (a[1:] > b[1:]) & (a[:-1] <= b[:-1])
    return out


def crossunder(a, b) -> np.ndarray:
    """True where a crosses below b (b may be a scalar)."""
    a, b = _pair(a, b)
    out = np.zeros(a.shape, dtype=bool)
    if len(a) > 1:
        with np.errstate(invalid="ignore"):
            out[1:] = (a[1:] < b[1:]) & (a[:-1] >= b[:-1])
    return out


def cross(a, b) -> np.ndarray:
    """True where a crosses b in either direction."""
    return crossover(a, b) | crossunder(a, b)
