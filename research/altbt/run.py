"""Fast offline backtest of a ctg strategy over a local OKX hourly panel (see README.md).

  python3 research/altbt/run.py strategies/library/alt_regime.py \
      --params '{"style": "aggressive", "margin_usdt": 125}' --deposit 3000 --from 2025-10-01 --to 2026-10-10
"""
import argparse
import csv
import datetime as dt
import json
import os
import sys
import time

import numpy as np

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from harness import H, Host, load_panel  # noqa: E402

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))


def ms(day: str) -> int:
    return int(dt.datetime.strptime(day, "%Y-%m-%d").replace(tzinfo=dt.timezone.utc).timestamp() * 1000)


def day(t: int) -> str:
    return dt.datetime.fromtimestamp(t / 1000, dt.timezone.utc).strftime("%Y-%m-%d")


def summarize(host: Host, margin_hint: float = 0.0) -> dict:
    eq = np.array([e for _, e in host.equity])
    ts = np.array([t for t, _ in host.equity])
    tr = host.trades
    pnl = np.array([t["pnl"] for t in tr]) if tr else np.zeros(0)
    peak = np.maximum.accumulate(eq)
    dd = eq - peak
    daily = eq[::24]
    r = np.diff(daily) / np.maximum(daily[:-1], 1e-9)
    months = {}
    for t, e in host.equity:
        months[day(t)[:7]] = e
    prev, monthly = host.deposit, {}
    for k in sorted(months):
        monthly[k] = round(months[k] - prev, 1)
        prev = months[k]
    margins = [t["margin"] for t in tr]
    out = {
        "from": day(int(ts[0])) if len(ts) else "", "to": day(int(ts[-1])) if len(ts) else "",
        "deposit": host.deposit, "final_equity": round(float(eq[-1]), 1) if len(eq) else host.deposit,
        "net": round(float(eq[-1] - host.deposit), 1) if len(eq) else 0.0,
        "max_drawdown": round(float(dd.min()), 1) if len(dd) else 0.0,
        "max_drawdown_pct": round(float((dd / peak).min()), 4) if len(dd) else 0.0,
        "min_equity": round(float(eq.min()), 1) if len(eq) else host.deposit,
        "sharpe": round(float(r.mean() / r.std() * np.sqrt(365)), 2) if len(r) > 5 and r.std() > 0 else None,
        "trades": len(tr), "win_rate": round(float((pnl > 0).mean()), 3) if len(pnl) else None,
        "profit_factor": round(float(pnl[pnl > 0].sum() / -pnl[pnl < 0].sum()), 2) if (pnl < 0).any() else None,
        "net_without_best5": round(float(pnl.sum() - np.sort(pnl)[-5:].sum()), 1) if len(pnl) >= 5 else None,
        "positive_months": round(float(np.mean([v > 0 for v in monthly.values()])), 2) if monthly else None,
        "long_pnl": round(float(sum(t["pnl"] for t in tr if t["side"] == "long")), 1),
        "short_pnl": round(float(sum(t["pnl"] for t in tr if t["side"] == "short")), 1),
        "fees": round(float(sum(t["fees"] for t in tr)), 1), "funding": round(float(sum(t["funding"] for t in tr)), 1),
        "exits": {k: sum(1 for t in tr if t["kind"] == k) for k in sorted({t["kind"] for t in tr})},
        "avg_margin": round(float(np.mean(margins)), 1) if margins else None,
        "avg_notional": round(float(np.mean([t["notional"] for t in tr])), 1) if tr else None,
        "signals": host.signals, "rejected": host.reject_reasons, "monthly": monthly,
        "errors": sum(1 for x in host.logs if x[1] == "error"),
    }
    if margin_hint:
        out["max_drawdown_in_margins"] = round(out["max_drawdown"] / margin_hint, 1)
    return out


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("strategy", help="strategy file, e.g. strategies/library/alt_regime.py")
    ap.add_argument("--class", dest="cls", default="", help="class name when the file has several")
    ap.add_argument("--params", default="{}", help="JSON of params (choice names / bools / numbers)")
    ap.add_argument("--panel", default=os.path.join(ROOT, "data", "altbt", "panel.npz"))
    ap.add_argument("--deposit", type=float, default=3000)
    ap.add_argument("--limits", default="{}", help="JSON overriding account limits (camelCase, as in the app)")
    ap.add_argument("--from", dest="start", default="", help="YYYY-MM-DD (UTC)")
    ap.add_argument("--to", dest="end", default="", help="YYYY-MM-DD (UTC), exclusive")
    ap.add_argument("--slip-bp", type=float, default=3.0, help="half spread + tick per market fill")
    ap.add_argument("--funding-default", type=float, default=0.0001,
                    help="rate per 8h settlement where the panel has no funding history (longs pay)")
    ap.add_argument("--out", default="", help="directory for summary.json, trades.csv, equity.csv")
    ap.add_argument("--quiet", action="store_true")
    a = ap.parse_args()

    t0 = time.time()
    P = load_panel(a.panel)
    host = Host(P, a.strategy, json.loads(a.params), a.cls, a.deposit, json.loads(a.limits),
                ms(a.start) if a.start else 0, ms(a.end) if a.end else 0, a.slip_bp,
                funding_default=a.funding_default)
    host.run(progress=not a.quiet)
    margins = [t["margin"] for t in host.trades]
    st = host.cls(host.params)
    hint = float(json.loads(a.params).get("margin_usdt") or 0) or (float(np.median(margins)) if margins else 0)
    s = summarize(host, hint)
    if hasattr(st, "suggested_margin"):
        s["suggested_margin_at_deposit"] = round(float(st.suggested_margin(a.deposit)), 1)
    s["seconds"] = round(time.time() - t0, 1)
    print(json.dumps({k: v for k, v in s.items() if k not in ("monthly", "rejected")}, ensure_ascii=False, indent=1))
    print("monthly:", " ".join(f"{k[2:]}:{v:+.0f}" for k, v in s["monthly"].items()))
    if s["rejected"]:
        print("rejected:", s["rejected"])
    if host.status:
        print("status:", host.status)
    errs = [x for x in host.logs if x[1] == "error"]
    if errs:
        print(f"{len(errs)} handler errors; first:\n{errs[0][2]}", file=sys.stderr)
    if a.out:
        os.makedirs(a.out, exist_ok=True)
        json.dump(s, open(os.path.join(a.out, "summary.json"), "w"), ensure_ascii=False, indent=1)
        if host.trades:
            with open(os.path.join(a.out, "trades.csv"), "w", newline="") as f:
                w = csv.DictWriter(f, fieldnames=list(host.trades[0]))
                w.writeheader()
                w.writerows(host.trades)
        with open(os.path.join(a.out, "equity.csv"), "w") as f:
            f.write("ts,equity\n")
            for t, e in host.equity[::4]:
                f.write(f"{t},{e:.2f}\n")


if __name__ == "__main__":
    main()
