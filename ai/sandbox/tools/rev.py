"""rev — move risk after an extended move, for OKX perpetuals (ctg sandbox tool).

  rev X [-b 1H|15m|4H]   for one coin: the validated move-risk probabilities (1H), the squeeze veto, and
                         the exhaustion checklist as a description of the move, with a fade reference
  rev scan [-n 6]        the pool's biggest 4h movers: which are in an extended state and how risky a fade is

What is and is not supported (2-year OKX 1H study, 19-month walk-forward out of sample):
  · No exhaustion sign or model predicts direction (AUC 0.47–0.53), the checklist included (0.49/0.50).
    Fading extended moves lost ~0.2% per trade, 59% stopped out; no market regime made it profitable.
  · P_tail (price runs ≥ 2 ATR past the 3-bar extreme within 24h = a fade's stop is hit) and P_big3
    (≥ 3 ATR either way within 24h) are predictable: AUC ≈ 0.63 / 0.62, above chance in 19 of 19 months.
  · Squeeze veto (funding ≤ −0.05%/settlement, or < 0 and > 15% above 1H EMA24): a short pays the
    negative funding; fades in big pumps averaged −0.72% vs 0.00%.

Reads ctg's open API (CTG_BASE, CTG_API_KEY): the scores are computed on the server (internal/market/
exhaustion.go, moverisk.go), so the app, the API and this tool show the same numbers.
"""

from __future__ import annotations

import json
import os
import sys
import urllib.parse
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta, timezone

BASE = os.environ.get("CTG_BASE", "https://ctg.gugenzzz.top/api/open/v1")
KEY = os.environ.get("CTG_API_KEY", "")
TZ = timezone(timedelta(hours=8))
FADE_BASE = "延伸状态里逆势单历史平均每笔约 −0.2%（含成本），59% 被止损"


def get(path: str, **q):
    url = f"{BASE}/{path}?{urllib.parse.urlencode(q)}"
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {KEY}"})
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.load(r)


def inst_id(x: str) -> str:
    x = x.upper()
    if x.endswith("-USDT-SWAP"):
        return x
    return x.removesuffix("USDT").removesuffix("-") + "-USDT-SWAP"


def fmt(v):
    a = abs(v)
    return f"{v:,.1f}" if a >= 1000 else f"{v:.4g}" if a < 1 else f"{v:.2f}" if a < 100 else f"{v:.1f}"


def risk_side(d: dict):
    """The side in an extended state (the larger extension if both), as (名, side dict)."""
    r = d.get("risk")
    if not r:
        return None
    sides = [(n, r[k]) for n, k in (("顶", "top"), ("底", "bottom")) if r[k]["inState"]]
    return max(sides, key=lambda x: x[1]["ext"]) if sides else None


def risk_lines(d: dict) -> list[str]:
    r = d.get("risk")
    if not r:
        return ["移动风险：1H 历史不足 150 根，无法计算"]
    rs = risk_side(d)
    if not rs:
        t, b = r["top"], r["bottom"]
        return [f"未处于延伸状态（顶 {t['ext']:+.1f} ATR · 底 {b['ext']:+.1f} ATR；需 ≥ 2 ATR、在 30 根区间边缘、24h 额 ≥ 200 万）：没有可用的统计"]
    name, s = rs
    tail, big = s["pTail"], s["pBig3"]
    lvl = "高" if tail >= 0.5 else "低" if tail < 0.25 else "中"
    return [f"{name}部延伸 {s['ext']:+.1f} ATR · 极值 {fmt(s['extreme'])}",
            f"延续风险 P_tail {tail:.0%}（基准 {s['baseTail']:.0%}，{lvl}）：24h 内再越过极值 2 ATR 的概率 = 逆势单止损被扫",
            f"波动幅度 P_big3 {big:.0%}（基准 {s['baseBig3']:.0%}）：24h 内任一方向走出 3 ATR 的概率，不代表方向"]


def report(d: dict) -> str:
    when = datetime.fromtimestamp(d["t"] / 1000, TZ).strftime("%m-%d %H:%M")
    out = [f"{d['instId']} {d['bar']} {when}  价格 {fmt(d['last'])} · ATR {fmt(d['atr'])}"]
    out += risk_lines(d)
    if d.get("squeeze"):
        out.append(f"⚠ 轧空否决，不做空：{d['squeezeWhy']}（做空要付负资金费，暴涨里逆势空单平均每笔 −0.72%）")
    top = d["top"] >= d["bottom"]
    items = d["topItems"] if top else d["bottomItems"]
    out.append(f"盘面描述（清单，历史检验无方向预测力）：{'顶' if top else '底'}部 {max(d['top'], d['bottom'])}/100")
    out += [f"  {'✓' if it['points'] >= it['weight'] * 0.6 else '·' if it['points'] > 0 else '✗'} {it['label']}" for it in items]
    p = d.get("plan")
    if p and max(d["top"], d["bottom"]) >= 40:
        side = "反手做空" if p["side"] == "short" else "反手做多"
        out.append(f"{side}参考：入场 {fmt(p['entryLo'])}–{fmt(p['entryHi'])} · 止损 {fmt(p['stop'])}（极值外 0.5 ATR）"
                   f" · 目标 {fmt(p['tp1'])}（EMA20，盈亏比 {p['rr']:.1f}）/ {fmt(p['tp2'])}（回撤 38.2%）")
        out.append(f"  基础统计：{FADE_BASE}；只在延续风险低且拐头已出现时考虑")
    return "\n".join(out)


def _safe(inst):
    try:
        return get("exhaustion", instId=inst, bar="1H")
    except Exception as e:  # noqa: BLE001
        return f"{inst}: {e}"


def scan(n: int) -> str:
    u = get("universe", limit=200, fields="ret_4h,ret_1h,vol_surprise_4h,funding_hourly")
    rows = u.get("rows", []) if isinstance(u, dict) else u
    rows = [r for r in rows if r.get("v", {}).get("ret_4h") is not None]
    rows.sort(key=lambda r: abs(r["v"]["ret_4h"]) * (1 + max(0, r["v"].get("vol_surprise_4h") or 0)), reverse=True)
    picks = [r["instId"] for r in rows[:n]]
    with ThreadPoolExecutor(3) as ex:
        res = [x for x in ex.map(_safe, picks) if isinstance(x, dict)]

    def key(d):
        rs = risk_side(d)
        return (0, rs[1]["pTail"]) if rs else (1, 0)

    lines = [f"近 4h 波动最大的 {len(picks)} 个币（延伸状态在前；延续风险越低，逆势单越不容易被扫）："]
    for d in sorted(res, key=key):
        rs = risk_side(d)
        sq = " · ⚠轧空否决" if d.get("squeeze") else ""
        if rs:
            name, s = rs
            lines.append(f"- {d['instId']}  {name}部延伸 {s['ext']:+.1f} ATR · 延续风险 {s['pTail']:.0%} · 波动 {s['pBig3']:.0%}{sq}")
        else:
            lines.append(f"- {d['instId']}  未处于延伸状态{sq}")
    lines.append(f"注意：方向无统计依据，{FADE_BASE}。`rev <币>` 看详情。")
    return "\n".join(lines)


def main(argv):
    if not argv or argv[0] in ("-h", "--help"):
        print(__doc__)
        return
    if argv[0] == "scan":
        n = int(argv[argv.index("-n") + 1]) if "-n" in argv else 6
        print(scan(min(max(n, 1), 10)))
        return
    bar = argv[argv.index("-b") + 1] if "-b" in argv else "1H"
    print(report(get("exhaustion", instId=inst_id(argv[0]), bar=bar)))


if __name__ == "__main__":
    try:
        main(sys.argv[1:])
    except Exception as e:  # noqa: BLE001
        print(f"rev: {e}", file=sys.stderr)
        sys.exit(1)
