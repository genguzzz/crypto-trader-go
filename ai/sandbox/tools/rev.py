"""rev — exhaustion / reversal check for OKX perpetuals (ctg sandbox tool).

  rev X [-b 1H|15m|4H]   top and bottom exhaustion scores (0–100) for one coin, the checklist behind
                         them and a fade plan (entry, stop beyond the extreme, targets)
  rev scan [-n 6]        the pool's biggest 4h movers, scored, best reversal candidates first

Reads the ctg open API (CTG_BASE, CTG_API_KEY). Plain text output, a few hundred characters per coin.
Scores are a checklist of classic blow-off / capitulation signs, not a validated model: in 6 months of
OKX alt pumps the single signs had direction AUC ≈ 0.5 (they flag big moves, not their direction). The
squeeze veto is the one rule with backtest support.
"""

from __future__ import annotations

import json
import math
import os
import statistics as st
import sys
import urllib.parse
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta, timezone

BASE = os.environ.get("CTG_BASE", "https://ctg.gugenzzz.top/api/open/v1")
KEY = os.environ.get("CTG_API_KEY", "")
TZ = timezone(timedelta(hours=8))


def get(path: str, **q):
    url = f"{BASE}/{path}?{urllib.parse.urlencode(q)}"
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {KEY}"})
    with urllib.request.urlopen(req, timeout=20) as r:
        return json.load(r)


def inst_id(x: str) -> str:
    x = x.upper()
    if x.endswith("-USDT-SWAP"):
        return x
    return x.removesuffix("USDT").removesuffix("-") + "-USDT-SWAP"


def ema(xs, n):
    k, out = 2 / (n + 1), []
    for x in xs:
        out.append(x if not out else out[-1] + k * (x - out[-1]))
    return out


def atr(cs, n=14):
    trs = [max(c["h"] - c["l"], abs(c["h"] - p["c"]), abs(c["l"] - p["c"])) for p, c in zip(cs, cs[1:])]
    return ema(trs, n)[-1] if trs else 0.0


def rsi(closes, n=14):
    out, up, dn = [None] * len(closes), 0.0, 0.0
    for i in range(1, len(closes)):
        d = closes[i] - closes[i - 1]
        g, l = max(d, 0), max(-d, 0)
        if i <= n:
            up, dn = up + g / n, dn + l / n
            if i < n:
                continue
        else:
            up, dn = (up * (n - 1) + g) / n, (dn * (n - 1) + l) / n
        out[i] = 100 if dn == 0 else 100 - 100 / (1 + up / dn)
    return out


def ramp(x, lo, hi):
    """0 at lo, 1 at hi (works for hi < lo too)."""
    if x is None:
        return 0.0
    t = (x - lo) / (hi - lo)
    return max(0.0, min(1.0, t))


def fmt(v):
    a = abs(v)
    return f"{v:,.1f}" if a >= 1000 else f"{v:.4g}" if a < 1 else f"{v:.2f}" if a < 100 else f"{v:.1f}"


def usd(v):
    return f"${v / 1e6:.1f}M" if abs(v) >= 1e6 else f"${v / 1e3:.0f}K"


def analyse(inst: str, bar: str = "1H") -> dict:
    with ThreadPoolExecutor(4) as ex:
        fc = ex.submit(get, "candles", instId=inst, bar=bar, limit=150)
        fl = ex.submit(get, "liquidations", instId=inst, bar=bar, limit=60, recent=0)
        fo = ex.submit(get, "orderflow", instId=inst, bar=bar, limit=24)
        fi = ex.submit(get, f"instruments/{inst}")
    cs = fc.result()
    cs = cs.get("data", cs) if isinstance(cs, dict) else cs
    if len(cs) < 40:
        raise RuntimeError("not enough candles")
    liq = {b["t"]: b for b in (fl.result().get("bars") or [])}
    flow = {b["t"]: b for b in (fo.result().get("bars") or [])}
    info = fi.result()
    row = info.get("row") or {}
    vals, pct = row.get("values") or {}, row.get("pct") or {}

    closes = [c["c"] for c in cs]
    e20 = ema(closes, 20)[-1]
    a = atr(cs) or 1e-12
    r = rsi(closes)
    last = cs[-1]
    recent = cs[-3:]
    hi_bar = max(recent, key=lambda c: c["h"])
    lo_bar = min(recent, key=lambda c: c["l"])
    base_hi, base_lo = max(c["h"] for c in cs[-30:-6]), min(c["l"] for c in cs[-30:-6])
    vols = [c["q"] for c in cs[-51:-3]]
    vmed = st.median(vols) if vols else 0
    vclimax = max(c["q"] for c in recent) / vmed if vmed else None
    rng = lambda c: max(c["h"] - c["l"], 1e-12)
    up_wick = (hi_bar["h"] - max(hi_bar["o"], hi_bar["c"])) / rng(hi_bar)
    lo_wick = (min(lo_bar["o"], lo_bar["c"]) - lo_bar["l"]) / rng(lo_bar)
    rsi_now = max(x for x in r[-3:] if x is not None)
    rsi_min = min(x for x in r[-3:] if x is not None)
    # divergence: the recent extreme beyond the prior swing while RSI does not confirm it
    prior = cs[-20:-4]
    p_hi = max(prior, key=lambda c: c["h"])
    p_lo = min(prior, key=lambda c: c["l"])
    idx = {c["t"]: i for i, c in enumerate(cs)}
    bear_div = hi_bar["h"] > p_hi["h"] and r[idx[hi_bar["t"]]] is not None and r[idx[p_hi["t"]]] is not None and r[idx[hi_bar["t"]]] < r[idx[p_hi["t"]]]
    bull_div = lo_bar["l"] < p_lo["l"] and r[idx[lo_bar["t"]]] is not None and r[idx[p_lo["t"]]] is not None and r[idx[lo_bar["t"]]] > r[idx[p_lo["t"]]]
    lq = [liq.get(c["t"], {}) for c in cs[-51:]]
    shorts = [b.get("short", 0) or 0 for b in lq]
    longs = [b.get("long", 0) or 0 for b in lq]
    s_recent, l_recent = sum(shorts[-3:]), sum(longs[-3:])
    s_med, l_med = st.median(shorts[:-3]) or 0, st.median(longs[:-3]) or 0
    delta3 = sum((flow.get(c["t"], {}).get("buy", 0) or 0) - (flow.get(c["t"], {}).get("sell", 0) or 0) for c in recent)
    fund8 = (vals.get("funding_hourly") or 0) * 8
    fpct = pct.get("funding_hourly")
    oi1, oi4 = vals.get("oi_chg_1h"), vals.get("oi_chg_4h")
    oi1 = math.expm1(oi1) if oi1 is not None else None  # log changes → simple
    oi4 = math.expm1(oi4) if oi4 is not None else None
    ext_hi = (hi_bar["h"] - e20) / a
    ext_lo = (e20 - lo_bar["l"]) / a
    run_up = (hi_bar["h"] - base_lo) / a
    run_dn = (base_hi - lo_bar["l"]) / a
    off_hi = (hi_bar["h"] - last["c"]) / a
    off_lo = (last["c"] - lo_bar["l"]) / a

    def item(ok_w, w, text):
        return (ok_w * w, w, text)

    top = [
        item(ramp(ext_hi, 1.5, 3.5), 15, f"高点偏离 EMA20 {ext_hi:+.1f} ATR"),
        item(ramp(run_up, 3, 8), 10, f"本轮涨幅 {run_up:.1f} ATR"),
        item(ramp(vclimax, 1.5, 4), 15, f"量能 {vclimax:.1f}x 中位" if vclimax else "量能 —"),
        item(max(ramp(up_wick, 0.25, 0.6), ramp(off_hi, 0.3, 1.2)), 15, f"上影 {up_wick:.0%} · 已从高点回落 {off_hi:.1f} ATR"),
        item(max(ramp(rsi_now, 65, 80), 1.0 if bear_div else 0), 10, f"RSI {rsi_now:.0f}" + (" · 顶背离" if bear_div else "")),
        item(ramp(s_recent / s_med if s_med else (3 if s_recent > 5e5 else 0), 2, 8) * ramp(s_recent, 1e5, 1e6), 15, f"空头爆仓 {usd(s_recent)}" + (f"（{s_recent / s_med:.0f}x 常态）" if s_med else "")),
        item(ramp(fpct, 70, 95) if fpct is not None else ramp(fund8, 0.0002, 0.0008), 10, f"资金费 {fund8 * 100:+.4f}%/8h" + (f" · {fpct:.0f} 分位" if fpct is not None else "")),
        item(1.0 if delta3 < 0 else 0.0, 10, f"近 3 根主动净{'卖' if delta3 < 0 else '买'} {usd(abs(delta3))}"),
    ]
    bot = [
        item(ramp(ext_lo, 1.5, 3.5), 15, f"低点偏离 EMA20 {-ext_lo:+.1f} ATR"),
        item(ramp(run_dn, 3, 8), 10, f"本轮跌幅 {run_dn:.1f} ATR"),
        item(ramp(vclimax, 1.5, 4), 15, f"量能 {vclimax:.1f}x 中位" if vclimax else "量能 —"),
        item(max(ramp(lo_wick, 0.25, 0.6), ramp(off_lo, 0.3, 1.2)), 15, f"下影 {lo_wick:.0%} · 已从低点反弹 {off_lo:.1f} ATR"),
        item(max(ramp(rsi_min, 35, 20), 1.0 if bull_div else 0), 10, f"RSI {rsi_min:.0f}" + (" · 底背离" if bull_div else "")),
        item(ramp(l_recent / l_med if l_med else (3 if l_recent > 5e5 else 0), 2, 8) * ramp(l_recent, 1e5, 1e6), 15, f"多头爆仓 {usd(l_recent)}" + (f"（{l_recent / l_med:.0f}x 常态）" if l_med else "")),
        item(ramp(fpct, 30, 5) if fpct is not None else ramp(fund8, -0.0002, -0.0008), 10, f"资金费 {fund8 * 100:+.4f}%/8h" + (f" · {fpct:.0f} 分位" if fpct is not None else "")),
        item(1.0 if delta3 > 0 else 0.0, 10, f"近 3 根主动净{'买' if delta3 > 0 else '卖'} {usd(abs(delta3))}"),
    ]
    # a top only counts near the top of the recent range (and a bottom near its floor): a coin that
    # already dumped from its high is past its top
    r_hi, r_lo = max(c["h"] for c in cs[-30:]), min(c["l"] for c in cs[-30:])
    span = max(r_hi - r_lo, 1e-12)
    at_top = ramp((hi_bar["h"] - r_lo) / span, 0.6, 0.92)
    at_bottom = ramp((r_hi - lo_bar["l"]) / span, 0.6, 0.92)
    score = lambda xs, gate: round(100 * gate * sum(x[0] for x in xs) / sum(x[1] for x in xs))
    # squeeze veto (pump-study backtest rule): funding ≤ −0.05%/settlement, or negative funding while price is
    # >15% above its 1H EMA24 — then the pump ran another +20% within 24h 43% of the time; never fade it
    fr = info.get("fundingRate")
    squeeze = ""
    if fr is not None:
        ext24 = None
        if bar == "1H":
            ext24 = closes[-1] / ema(closes, 24)[-1] - 1
        else:
            h1 = get("candles", instId=inst, bar="1H", limit=60)
            h1 = h1.get("data", h1) if isinstance(h1, dict) else h1
            if len(h1) >= 24:
                c1 = [c["c"] for c in h1]
                ext24 = c1[-1] / ema(c1, 24)[-1] - 1
        if fr <= -0.0005:
            squeeze = f"资金费 {fr * 100:.3f}%/期，空头拥挤（轧空燃料）"
        elif fr < 0 and ext24 is not None and ext24 > 0.15:
            squeeze = f"资金费为负且高于 1H EMA24 {ext24:.0%}"
    return {
        "squeeze": squeeze,
        "inst": inst, "bar": bar, "last": last["c"], "t": last["t"], "atr": a, "ema20": e20,
        "top": score(top, at_top), "bottom": score(bot, at_bottom), "top_items": top, "bot_items": bot,
        "off_hi": off_hi, "off_lo": off_lo,
        "hi": hi_bar["h"], "lo": lo_bar["l"], "base_lo": base_lo, "base_hi": base_hi,
        "oi1": oi1, "oi4": oi4, "ret4": math.expm1(vals["ret_4h"]) if vals.get("ret_4h") is not None else None,
    }


def plan_lines(d: dict) -> list[str]:
    a = d["atr"]
    if d["top"] >= d["bottom"]:
        stop = d["hi"] + 0.5 * a
        e_lo, e_hi = max(d["last"], d["hi"] - 1.0 * a), d["hi"] - 0.2 * a
        t1, t2 = d["ema20"], d["base_lo"] + 0.382 * (d["hi"] - d["base_lo"])
        side = "反手做空"
    else:
        stop = d["lo"] - 0.5 * a
        e_lo, e_hi = d["lo"] + 0.2 * a, min(d["last"], d["lo"] + 1.0 * a)
        t1, t2 = d["ema20"], d["base_hi"] - 0.382 * (d["base_hi"] - d["lo"])
        side = "反手做多"
    mid = (e_lo + e_hi) / 2
    rr = abs(t1 - mid) / abs(mid - stop) if mid != stop else 0
    return [f"{side}参考：入场 {fmt(min(e_lo, e_hi))}–{fmt(max(e_lo, e_hi))} · 止损 {fmt(stop)}（极值外 0.5 ATR，距入场 {abs(stop / mid - 1):.1%}）"
            f" · 目标 {fmt(t1)}（EMA20，盈亏比 {rr:.1f}）/ {fmt(t2)}（回撤 38.2%）"]


def verdict(s: int) -> str:
    return "强衰竭" if s >= 70 else "有衰竭迹象" if s >= 50 else "弱" if s >= 30 else "无"


def report(d: dict, detail: bool = True) -> str:
    when = datetime.fromtimestamp(d["t"] / 1000, TZ).strftime("%m-%d %H:%M")
    oi = f" · OI 1h {d['oi1']:+.1%} 4h {d['oi4']:+.1%}" if d["oi1"] is not None and d["oi4"] is not None else ""
    out = [f"{d['inst']} {d['bar']} {when}  价格 {fmt(d['last'])} · ATR {fmt(d['atr'])}{oi}",
           f"顶部衰竭 {d['top']}/100（{verdict(d['top'])}） · 底部衰竭 {d['bottom']}/100（{verdict(d['bottom'])}）"]
    if detail:
        items = d["top_items"] if d["top"] >= d["bottom"] else d["bot_items"]
        out += [f"  {'✓' if got >= w * 0.6 else '·' if got > 0 else '✗'} {text}" for got, w, text in items]
    if d.get("squeeze"):
        out.append(f"⚠ 轧空风险，不做空：{d['squeeze']}（回测：此状态 24h 内再涨 20% 的概率 43%，全部暴涨状态为 29%）")
    if max(d["top"], d["bottom"]) >= 40 and not (d.get("squeeze") and d["top"] >= d["bottom"]):
        off = d["off_hi"] if d["top"] >= d["bottom"] else d["off_lo"]
        stage = ("仍在极值附近：等确认（长影线、放量回落/回升、主动买卖翻转）再进" if off < 0.3 else
                 "刚拐头：介入窗口" if off <= 1.5 else f"已走出 {off:.1f} ATR：不追，等回踩入场区")
        out.append(f"阶段：{stage}")
        out += plan_lines(d)
    return "\n".join(out)


def scan(n: int) -> str:
    u = get("universe", limit=200, fields="ret_4h,ret_1h,vol_surprise_4h,funding_hourly")
    rows = u.get("rows", [])
    rows = [r for r in rows if r.get("v", {}).get("ret_4h") is not None]
    rows.sort(key=lambda r: abs(r["v"]["ret_4h"]) * (1 + max(0, r["v"].get("vol_surprise_4h") or 0)), reverse=True)
    picks = [r["instId"] for r in rows[:n]]
    with ThreadPoolExecutor(3) as ex:
        res = list(ex.map(lambda i: _safe(i), picks))
    res = [r for r in res if isinstance(r, dict)]
    res.sort(key=lambda d: max(d["top"], d["bottom"]), reverse=True)
    lines = [f"近 4h 波动最大的 {len(picks)} 个币的衰竭评分（高分在前）："]
    for d in res:
        side = "顶" if d["top"] >= d["bottom"] else "底"
        r4 = f"4h {d['ret4']:+.1%} · " if d["ret4"] is not None else ""
        off = d["off_hi"] if side == "顶" else d["off_lo"]
        lines.append(f"- {d['inst']}  {r4}{side}部衰竭 {max(d['top'], d['bottom'])}（{verdict(max(d['top'], d['bottom']))}）· 距极值 {off:.1f} ATR")
    lines.append("对高分的币再跑 `rev <币>` 看清单和反手计划。")
    return "\n".join(lines)


def _safe(inst):
    try:
        return analyse(inst)
    except Exception as e:  # noqa: BLE001
        return f"{inst}: {e}"


def main(argv):
    if not argv or argv[0] in ("-h", "--help"):
        print(__doc__)
        return
    if argv[0] == "scan":
        n = int(argv[argv.index("-n") + 1]) if "-n" in argv else 6
        print(scan(min(max(n, 1), 10)))
        return
    bar = argv[argv.index("-b") + 1] if "-b" in argv else "1H"
    print(report(analyse(inst_id(argv[0]), bar)))


if __name__ == "__main__":
    try:
        main(sys.argv[1:])
    except Exception as e:  # noqa: BLE001
        print(f"rev: {e}", file=sys.stderr)
        sys.exit(1)
