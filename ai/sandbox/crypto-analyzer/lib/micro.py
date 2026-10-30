"""Market microstructure analytics on CTG/OKX payloads.

Order book: depth bands, imbalance, walls (support/resistance or spoof candidates), cost to move.
Order flow: CVD, taker-buy ratio, price/CVD divergence, Kyle's λ (price impact per $ net flow),
            Amihud illiquidity, VPIN-style flow toxicity (Easley, López de Prado, O'Hara 2012).
Liquidations: realized long/short liquidations, estimated liquidation clusters (magnets).
"""

from __future__ import annotations

import math
from statistics import median


# ── order book ─────────────────────────────────────────────────

def book_stats(book: dict) -> dict:
    bids, asks = book.get("bids") or [], book.get("asks") or []
    if not bids or not asks:
        return {}
    mid = book.get("mid") or (bids[0][0] + asks[0][0]) / 2

    def band(levels, pct, is_bid):
        lim = mid * (1 - pct / 100) if is_bid else mid * (1 + pct / 100)
        return sum(l[1] for l in levels if (l[0] >= lim if is_bid else l[0] <= lim))

    bands = {}
    for pct in (0.5, 1, 2, 5, 10):
        b, a = band(bids, pct, True), band(asks, pct, False)
        bands[str(pct)] = {"bid": b, "ask": a, "imb": b / (b + a) if b + a else None}

    def walls(levels, is_bid):
        near = [l for l in levels if abs(l[0] / mid - 1) <= 0.10]
        if len(near) < 5:
            return []
        med = median(l[1] for l in near) or 0
        tot = sum(l[1] for l in near)
        thr = max(5 * med, 0.03 * tot)
        big = sorted((l for l in near if l[1] >= thr), key=lambda l: l[1], reverse=True)[:4]
        return [{"px": l[0], "usd": l[1], "dist_pct": (l[0] / mid - 1) * 100, "x_median": l[1] / med if med else None}
                for l in big]

    return {
        "mid": mid, "spread_bps": book.get("spreadBps") or (asks[0][0] - bids[0][0]) / mid * 1e4,
        "bands": bands,
        "imb_1": bands["1"]["imb"], "imb_2": bands["2"]["imb"],
        "cost_up_2pct": bands["2"]["ask"], "cost_down_2pct": bands["2"]["bid"],
        "bid_walls": walls(bids, True), "ask_walls": walls(asks, False),
        "levels": len(bids) + len(asks),
    }


# ── order flow ─────────────────────────────────────────────────

def _ols_slope(xs: list[float], ys: list[float]) -> float | None:
    n = len(xs)
    if n < 8:
        return None
    mx, my = sum(xs) / n, sum(ys) / n
    sxx = sum((x - mx) ** 2 for x in xs)
    if sxx == 0:
        return None
    return sum((x - mx) * (y - my) for x, y in zip(xs, ys)) / sxx


def flow_stats(flow_bars: list[dict], candles: list[dict]) -> dict:
    """flow_bars: [{t,buy,sell}] ; candles: [{t,o,c,q}] same bar size."""
    if not flow_bars:
        return {}
    cmap = {c["t"]: c for c in candles}
    bars = [b for b in flow_bars if b.get("buy") is not None and b.get("sell") is not None]
    if not bars:
        return {}

    def window(n):
        w = bars[-n:]
        buy, sell = sum(b["buy"] for b in w), sum(b["sell"] for b in w)
        c0, c1 = cmap.get(w[0]["t"]), cmap.get(w[-1]["t"])
        px = (c1["c"] / c0["o"] - 1) * 100 if c0 and c1 and c0["o"] else None
        return {"bars": len(w), "delta": buy - sell, "buy_ratio": buy / (buy + sell) if buy + sell else None,
                "price_chg_pct": px}

    w4, w24 = window(4), window(24)

    # Kyle's λ: bar log-return vs net taker flow ($M) → bps per $1M
    xs, ys, amihud, vpin = [], [], [], []
    for b in bars[-48:]:
        c = cmap.get(b["t"])
        tot = b["buy"] + b["sell"]
        if tot > 0:
            vpin.append(abs(b["buy"] - b["sell"]) / tot)
        if not c or c["o"] <= 0:
            continue
        r = math.log(c["c"] / c["o"])
        xs.append((b["buy"] - b["sell"]) / 1e6)
        ys.append(r * 1e4)
        if tot > 0:
            amihud.append(abs(r) * 1e4 / (tot / 1e6))
    lam = _ols_slope(xs, ys)

    div = None
    p = w24["price_chg_pct"]
    if p is not None and w24["buy_ratio"] is not None:
        if p > 2 and w24["buy_ratio"] < 0.48:
            div = "bearish"   # 价涨但主动卖占优：上涨靠挂单托/现货带，追多无主动买盘支撑
        elif p < -2 and w24["buy_ratio"] > 0.52:
            div = "bullish"   # 价跌但主动买占优：下方有人吸收抛压（吸筹/空头被吸收）
    cvd, run = [], 0.0
    for b in bars:
        run += b["buy"] - b["sell"]
        cvd.append(run)
    return {
        "w4": w4, "w24": w24, "divergence": div,
        "kyle_lambda_bps_per_m": lam,
        "amihud_bps_per_m": median(amihud) if amihud else None,
        "vpin": sum(vpin[-24:]) / len(vpin[-24:]) if vpin else None,
        "cvd_last": cvd[-1], "cvd_series": cvd[-48:],
    }


# ── liquidations ───────────────────────────────────────────────

def liq_stats(liq: dict, now_ms: int) -> dict:
    bars = liq.get("bars") or []

    def since(hours):
        cut = now_ms - hours * 3600_000
        w = [b for b in bars if b["t"] >= cut]
        return {"long": sum(b.get("long") or 0 for b in w), "short": sum(b.get("short") or 0 for b in w)}

    recent = sorted(liq.get("recent") or [], key=lambda r: r.get("usd") or 0, reverse=True)[:5]
    return {"h4": since(4), "h24": since(24), "h48": since(48), "largest_recent": recent}


def liqmap_stats(lm: dict) -> dict:
    px, bins = lm.get("px"), lm.get("bins") or []
    if not px or not bins:
        return {}

    def tot(side, lo, hi):
        return sum(b[side] for b in bins if lo <= (b["p"] / px - 1) * 100 <= hi)

    above = [b for b in bins if b["p"] > px and b.get("short")]
    below = [b for b in bins if b["p"] < px and b.get("long")]
    top_above = sorted(above, key=lambda b: b["short"], reverse=True)[:3]
    top_below = sorted(below, key=lambda b: b["long"], reverse=True)[:3]
    s5, l5 = tot("short", 0, 5), tot("long", -5, 0)
    s10, l10 = tot("short", 0, 10), tot("long", -10, 0)
    return {
        "px": px,
        "short_liq_above_5": s5, "long_liq_below_5": l5,
        "short_liq_above_10": s10, "long_liq_below_10": l10,
        "magnet_ratio_10": s10 / l10 if l10 else None,  # >1 上方空头清算更多 → 向上猎杀倾向
        "short_clusters": [{"px": b["p"], "usd": b["short"], "dist_pct": (b["p"] / px - 1) * 100} for b in top_above],
        "long_clusters": [{"px": b["p"], "usd": b["long"], "dist_pct": (b["p"] / px - 1) * 100} for b in top_below],
        "note": "估算值：由 OI 增量 × 假设杠杆分布推算，非交易所真实强平价",
    }
