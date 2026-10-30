"""Technical indicators — pure Python, no pandas."""

from __future__ import annotations

from typing import Sequence


def sma(values: Sequence[float], period: int) -> list[float | None]:
    out: list[float | None] = [None] * len(values)
    if period <= 0:
        return out
    s = 0.0
    for i, v in enumerate(values):
        s += v
        if i >= period:
            s -= values[i - period]
        if i >= period - 1:
            out[i] = s / period
    return out


def ema(values: Sequence[float], period: int) -> list[float | None]:
    out: list[float | None] = [None] * len(values)
    if period <= 0 or len(values) < period:
        return out
    k = 2 / (period + 1)
    seed = sum(values[:period]) / period
    out[period - 1] = seed
    prev = seed
    for i in range(period, len(values)):
        prev = values[i] * k + prev * (1 - k)
        out[i] = prev
    return out


def rsi(closes: Sequence[float], period: int = 14) -> list[float | None]:
    out: list[float | None] = [None] * len(closes)
    if len(closes) <= period:
        return out
    gains, losses = [], []
    for i in range(1, len(closes)):
        d = closes[i] - closes[i - 1]
        gains.append(max(d, 0))
        losses.append(max(-d, 0))
    avg_gain = sum(gains[:period]) / period
    avg_loss = sum(losses[:period]) / period
    if avg_loss == 0:
        out[period] = 100.0
    else:
        rs = avg_gain / avg_loss
        out[period] = 100 - 100 / (1 + rs)
    for i in range(period + 1, len(closes)):
        avg_gain = (avg_gain * (period - 1) + gains[i - 1]) / period
        avg_loss = (avg_loss * (period - 1) + losses[i - 1]) / period
        if avg_loss == 0:
            out[i] = 100.0
        else:
            rs = avg_gain / avg_loss
            out[i] = 100 - 100 / (1 + rs)
    return out


def macd(closes: Sequence[float], fast: int = 12, slow: int = 26, signal: int = 9) -> dict[str, list[float | None]]:
    ema_fast = ema(closes, fast)
    ema_slow = ema(closes, slow)
    line: list[float | None] = [None] * len(closes)
    for i in range(len(closes)):
        if ema_fast[i] is not None and ema_slow[i] is not None:
            line[i] = ema_fast[i] - ema_slow[i]
    valid_line = [x if x is not None else 0.0 for x in line]
    sig = ema(valid_line, signal)
    hist: list[float | None] = [None] * len(closes)
    for i in range(len(closes)):
        if line[i] is not None and sig[i] is not None:
            hist[i] = line[i] - sig[i]
    return {"macd": line, "signal": sig, "histogram": hist}


def bollinger(closes: Sequence[float], period: int = 20, std_mult: float = 2.0) -> dict[str, list[float | None]]:
    mid = sma(closes, period)
    upper: list[float | None] = [None] * len(closes)
    lower: list[float | None] = [None] * len(closes)
    for i in range(len(closes)):
        if mid[i] is None or i < period - 1:
            continue
        window = closes[i - period + 1 : i + 1]
        mean = mid[i]
        var = sum((x - mean) ** 2 for x in window) / period
        sd = var ** 0.5
        upper[i] = mean + std_mult * sd
        lower[i] = mean - std_mult * sd
    return {"middle": mid, "upper": upper, "lower": lower}


def compute_all(closes: Sequence[float], rsi_period: int = 14) -> dict:
    return {
        "rsi": rsi(closes, rsi_period),
        "macd": macd(closes),
        "bb": bollinger(closes),
        "ema12": ema(closes, 12),
        "ema26": ema(closes, 26),
        "sma20": sma(closes, 20),
    }


def last_valid(values: Sequence[float | None]) -> float | None:
    for v in reversed(values):
        if v is not None:
            return v
    return None


# ── candle-based (rows: dicts with o/h/l/c/q, oldest first) ────

def atr(rows: Sequence[dict], period: int = 14) -> float | None:
    if len(rows) < period + 1:
        return None
    trs = []
    for i in range(1, len(rows)):
        h, l, pc = rows[i]["h"], rows[i]["l"], rows[i - 1]["c"]
        trs.append(max(h - l, abs(h - pc), abs(l - pc)))
    a = sum(trs[:period]) / period
    for tr in trs[period:]:
        a = (a * (period - 1) + tr) / period  # Wilder smoothing
    return a


def efficiency_ratio(closes: Sequence[float], n: int) -> float | None:
    """Kaufman ER: |net move| / sum |moves|. 1 = straight line, 0 = chop."""
    if len(closes) <= n:
        return None
    w = closes[-n - 1:]
    path = sum(abs(w[i] - w[i - 1]) for i in range(1, len(w)))
    return abs(w[-1] - w[0]) / path if path else 0.0


def realized_vol(closes: Sequence[float], n: int) -> float | None:
    """Stdev of log returns over the last n bars (per bar, not annualized)."""
    import math
    if len(closes) <= n:
        return None
    rets = [math.log(closes[i] / closes[i - 1]) for i in range(len(closes) - n, len(closes)) if closes[i - 1] > 0]
    if len(rets) < 2:
        return None
    m = sum(rets) / len(rets)
    return (sum((r - m) ** 2 for r in rets) / (len(rets) - 1)) ** 0.5


def swings(rows: Sequence[dict], k: int = 3) -> dict:
    """Fractal swing highs/lows (k bars each side), most recent first."""
    highs, lows = [], []
    for i in range(k, len(rows) - k):
        h, l = rows[i]["h"], rows[i]["l"]
        if all(h >= rows[j]["h"] for j in range(i - k, i + k + 1)):
            highs.append(h)
        if all(l <= rows[j]["l"] for j in range(i - k, i + k + 1)):
            lows.append(l)
    return {"highs": highs[::-1][:5], "lows": lows[::-1][:5]}


def wick_spikes(rows: Sequence[dict], atr_v: float | None, n: int = 48) -> dict:
    """插针: bars whose wick ≥ 2×body and ≥ 1.5×ATR. Typical of stop/liquidation hunting."""
    up = down = 0
    if not atr_v:
        return {"up": 0, "down": 0}
    for r in rows[-n:]:
        body = abs(r["c"] - r["o"])
        uw = r["h"] - max(r["c"], r["o"])
        lw = min(r["c"], r["o"]) - r["l"]
        if uw >= 2 * body and uw >= 1.5 * atr_v:
            up += 1
        if lw >= 2 * body and lw >= 1.5 * atr_v:
            down += 1
    return {"up": up, "down": down}


def vwap(rows: Sequence[dict]) -> float | None:
    """Σ(tp·vol)/Σvol with base vol ≈ quote turnover / tp  ⇒  Σq / Σ(q/tp)."""
    den = sum((r.get("q") or 0) / ((r["h"] + r["l"] + r["c"]) / 3) for r in rows if r["c"] > 0)
    tot_q = sum(r.get("q") or 0 for r in rows)
    return tot_q / den if den else None
