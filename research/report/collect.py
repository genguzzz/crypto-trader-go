"""Collect research/altbt run outputs into the JSON the research report renders.

  python3 research/report/collect.py out/g_c3000_m125:保守·每笔125U out/g_a3000_m125:激进·每笔125U ... \
      > research/report/results/alt-regime.json

Each argument is an altbt --out directory and a display label. Per scenario: the summary, monthly
P&L, a daily equity curve, the ten largest trades, the R distribution, P&L by leg and, for runs of
at least a year, start-date risk (for every start day, trades opened in the next 365 days)."""
import csv
import datetime as dt
import json
import os
import sys

import numpy as np

DAY = 86400_000


def day(t):
    return dt.datetime.fromtimestamp(int(t) / 1000, dt.timezone.utc).strftime("%Y-%m-%d")


def leg_of(reason):
    return "弱势做空" if "最弱" in reason else ("突破做空" if "跌破" in reason else "突破做多")


def scenario(path, label):
    s = json.load(open(os.path.join(path, "summary.json")))
    eq = []
    with open(os.path.join(path, "equity.csv")) as f:
        rd = csv.reader(f)
        next(rd)
        last = None
        for t, e in rd:
            d = int(t) // DAY
            if d != last:
                eq.append([int(t), round(float(e), 1)])
                last = d
    tr = list(csv.DictReader(open(os.path.join(path, "trades.csv")))) if os.path.exists(os.path.join(path, "trades.csv")) else []
    rows = []
    for t in tr:
        risk = float(t["qty"]) * abs(float(t["entry"]) - float(t["stop"]))
        rows.append(dict(inst=t["inst"].replace("-USDT-SWAP", ""), side=t["side"], t_in=int(t["t_in"]), t_out=int(t["t_out"]),
                         pnl=float(t["pnl"]), R=float(t["pnl"]) / risk if risk > 0 else 0.0, kind=t["kind"], leg=leg_of(t["reason"])))
    pnl = np.array([r["pnl"] for r in rows]) if rows else np.zeros(0)
    top = sorted(rows, key=lambda r: -r["pnl"])[:10]
    worst = sorted(rows, key=lambda r: r["pnl"])[:5]
    edges = [-3, -1.5, -1, -0.5, 0, 0.5, 1, 2, 5, 10, 1e9]
    names = ["<-1.5", "-1.5~-1", "-1~-0.5", "-0.5~0", "0~0.5", "0.5~1", "1~2", "2~5", "5~10", ">10"]
    Rs = np.array([r["R"] for r in rows]) if rows else np.zeros(0)
    hist = [int(((Rs >= lo) & (Rs < hi)).sum()) for lo, hi in zip(edges[:-1], edges[1:])]
    legs = {}
    for r in rows:
        g = legs.setdefault(r["leg"], {"n": 0, "pnl": 0.0, "win": 0})
        g["n"] += 1
        g["pnl"] += r["pnl"]
        g["win"] += r["pnl"] > 0
    cum_sorted = np.cumsum(np.sort(pnl)[::-1]).tolist() if len(pnl) else []
    starts = None
    if rows and (max(r["t_out"] for r in rows) - min(r["t_in"] for r in rows)) > 400 * DAY:
        t_in = np.array([r["t_in"] for r in rows])
        t_out = np.array([r["t_out"] for r in rows])
        dips, years, ds = [], [], []
        for s0 in range(int(t_in.min()), int(t_out.max() - 365 * DAY), DAY):
            sel = (t_in >= s0) & (t_in < s0 + 365 * DAY)
            o = np.argsort(t_out[sel])
            cum = np.cumsum(pnl[sel][o])
            dips.append(round(float(min(0.0, cum.min())) if len(cum) else 0.0, 1))
            years.append(round(float(cum[-1]) if len(cum) else 0.0, 1))
            ds.append(day(s0))
        starts = {"day": ds, "dip": dips, "year": years}
    keep = ("from", "to", "deposit", "final_equity", "net", "max_drawdown", "max_drawdown_pct", "min_equity", "sharpe",
            "trades", "win_rate", "profit_factor", "net_without_best5", "positive_months", "long_pnl", "short_pnl", "fees",
            "funding", "exits", "avg_margin", "avg_notional", "suggested_margin_at_deposit")
    return dict(id=os.path.basename(path.rstrip("/")), label=label, summary={k: s.get(k) for k in keep},
                monthly=s.get("monthly", {}), equity=eq, top=top, worst=worst, r_hist={"bins": names, "n": hist},
                legs={k: {"n": v["n"], "pnl": round(v["pnl"], 1), "win": round(v["win"] / v["n"], 3)} for k, v in legs.items()},
                concentration=[round(x, 1) for x in cum_sorted[:50]], net=float(pnl.sum()) if len(pnl) else 0.0, starts=starts)


if __name__ == "__main__":
    out = []
    for a in sys.argv[1:]:
        path, _, label = a.partition(":")
        out.append(scenario(path, label or os.path.basename(path)))
    json.dump({"generated": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%d %H:%M UTC"), "scenarios": out},
              sys.stdout, ensure_ascii=False, separators=(",", ":"))
