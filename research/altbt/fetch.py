"""Download an hourly panel of OKX USDT swaps (public market data only) for the offline backtest.

  python3 research/altbt/fetch.py --from 2024-10-01            # → data/altbt/panel.npz
  HTTPS_PROXY=http://127.0.0.1:7080 python3 research/altbt/fetch.py --from 2025-10-01 --insts BTC,SOL,DOGE

Per-instrument downloads are cached in data/altbt/raw/ (re-runs only fetch what is missing or new).
Panel arrays: ts[T] (hour open, ms), insts[N], crypto[N] (instCategory 1), o/h/l/c/q[T,N] float32
(q = USDT turnover), fund[T,N] settled funding rate at its settlement hour (NaN otherwise).
Instruments delisted before the download are not available (survivorship bias)."""
import argparse
import datetime as dt
import json
import os
import sys
import threading
import time
import urllib.request
from concurrent.futures import ThreadPoolExecutor

import numpy as np

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
BASE = "https://www.okx.com/api/v5/"
H = 3600_000
_opener = urllib.request.build_opener(urllib.request.ProxyHandler())   # honours HTTPS_PROXY
_opener.addheaders = [("User-Agent", "Mozilla/5.0 (ctg research)")]    # OKX rejects urllib's default agent


class Rate:
    """Simple shared rate limiter (OKX history-candles allows ~10 requests/s per IP)."""

    def __init__(self, per_sec):
        self.gap, self.t, self.lock = 1 / per_sec, 0.0, threading.Lock()

    def wait(self):
        with self.lock:
            now = time.time()
            w = max(0.0, self.t + self.gap - now)
            self.t = now + w
        time.sleep(w)


CANDLES, FUNDING = Rate(8), Rate(4)


def get(path, rate):
    for k in range(8):
        rate.wait()
        try:
            d = json.load(_opener.open(BASE + path, timeout=20))
            if d.get("code") == "0":
                return d["data"]
        except Exception:
            pass
        time.sleep(1 + k)
    print("failed:", path, file=sys.stderr)
    return None


def candles(inst, start, end):
    rows, cur = {}, f"&after={end + H}"
    while True:
        d = get(f"market/history-candles?bar=1H&limit=100&instId={inst}{cur}", CANDLES)
        if not d:
            break
        for r in d:
            t = int(r[0])
            if r[8] == "1" and start <= t <= end:
                rows[t] = [float(r[1]), float(r[2]), float(r[3]), float(r[4]), float(r[7])]
        last = int(d[-1][0])
        if last <= start or len(d) < 100:
            break
        cur = f"&after={last}"
    return rows


def funding(inst, start):
    rows, cur = {}, ""
    while True:
        d = get(f"public/funding-rate-history?limit=100&instId={inst}{cur}", FUNDING)
        if not d:
            break
        for x in d:
            rows[int(x["fundingTime"])] = float(x["realizedRate"] or x["fundingRate"])
        last = int(d[-1]["fundingTime"])
        if last <= start or len(d) < 100:
            break
        cur = f"&after={last}"
    return rows


def one(inst, start, end, raw):
    fn = os.path.join(raw, inst + ".json")
    old = json.load(open(fn)) if os.path.exists(fn) else {"c": {}, "f": {}}
    have = {int(k): v for k, v in old["c"].items()}
    fund = {int(k): v for k, v in old["f"].items()}
    lo = min(have) if have else None
    hi = max(have) if have else None
    if lo is None or lo > start:
        have.update(candles(inst, start, (lo - H) if lo else end))
    if hi is None or hi < end:
        have.update(candles(inst, (hi + H) if hi else start, end))
    if not fund or max(fund) < end - 8 * H:
        fund.update(funding(inst, start))
    json.dump({"c": {str(k): v for k, v in have.items()}, "f": {str(k): v for k, v in fund.items()}}, open(fn + ".tmp", "w"))
    os.replace(fn + ".tmp", fn)
    return inst, have, fund


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--from", dest="start", default="2024-10-01")
    ap.add_argument("--to", dest="end", default="", help="default: the last closed hour")
    ap.add_argument("--insts", default="", help="comma-separated bases or instIds (default: every live USDT swap)")
    ap.add_argument("--out", default=os.path.join(ROOT, "data", "altbt", "panel.npz"))
    ap.add_argument("--workers", type=int, default=12)
    a = ap.parse_args()
    start = int(dt.datetime.strptime(a.start, "%Y-%m-%d").replace(tzinfo=dt.timezone.utc).timestamp() * 1000)
    end = (int(dt.datetime.strptime(a.end, "%Y-%m-%d").replace(tzinfo=dt.timezone.utc).timestamp() * 1000) - H
           if a.end else int(time.time() * 1000) // H * H - H)
    raw = os.path.join(os.path.dirname(a.out), "raw")
    os.makedirs(raw, exist_ok=True)
    meta = [x for x in get("public/instruments?instType=SWAP", CANDLES) or []
            if x["instId"].endswith("-USDT-SWAP") and x["state"] == "live"]
    cat = {x["instId"]: x.get("instCategory") == "1" for x in meta}
    insts = sorted(cat)
    if a.insts:
        want = {s if s.endswith("-USDT-SWAP") else f"{s.upper()}-USDT-SWAP" for s in a.insts.split(",")}
        insts = [i for i in insts if i in want]
    print(f"{len(insts)} instruments, {a.start} → {dt.datetime.fromtimestamp(end / 1000, dt.timezone.utc):%Y-%m-%d %H:00}",
          file=sys.stderr)
    with ThreadPoolExecutor(a.workers) as ex:
        res = list(ex.map(lambda s: one(s, start, end, raw), insts))
    ts = np.arange(start, end + H, H, dtype=np.int64)
    T, N = len(ts), len(res)
    arr = {k: np.full((T, N), np.nan, np.float32) for k in "ohlcq"}
    fund = np.full((T, N), np.nan, np.float32)
    for j, (inst, bars, fr) in enumerate(res):
        for t, v in bars.items():
            i = (t - start) // H
            if 0 <= i < T:
                for k, x in zip("ohlcq", v):
                    arr[k][i, j] = x
        for t, r in fr.items():
            i = (t - start) // H
            if 0 <= i < T:
                fund[i, j] = r
    np.savez_compressed(a.out, ts=ts, insts=np.array([r[0] for r in res]),
                        crypto=np.array([cat[r[0]] for r in res]), fund=fund, **arr)
    print(f"wrote {a.out}: {T} hours × {N} instruments", file=sys.stderr)


if __name__ == "__main__":
    main()
