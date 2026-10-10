"""Download an hourly panel of OKX USDT swaps (public market data only) for the offline backtest.

  python3 research/altbt/fetch.py --from 2024-10-01            # → data/altbt/panel.npz
  HTTPS_PROXY=http://127.0.0.1:7080 python3 research/altbt/fetch.py --from 2025-10-01 --insts BTC,SOL,DOGE

Per-instrument downloads are cached in data/altbt/raw/ (re-runs only fetch what is missing or new).
With --update an existing panel is extended in place instead: every instrument is fetched from its
last stored hour to now (new listings from the panel start); instruments delisted since then keep
their history, so a panel updated over time loses the survivorship bias going forward.
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
FAILED: list[str] = []          # requests that failed after all retries (reported at the end)


class Incomplete(Exception):
    """A paginated download stopped on a failed request: the data would have a hole."""


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
    FAILED.append(path)
    return None


def candles(inst, start, end):
    rows, cur = {}, f"&after={end + H}"
    while True:
        d = get(f"market/history-candles?bar=1H&limit=100&instId={inst}{cur}", CANDLES)
        if d is None:
            raise Incomplete(inst)
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
        if d is None:
            raise Incomplete(inst)
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
    try:
        return _one(inst, start, end, raw)
    except Incomplete:
        print(f"{inst}: download incomplete, cache not updated (re-run to retry)", file=sys.stderr)
        fn = os.path.join(raw, inst + ".json")
        old = json.load(open(fn)) if os.path.exists(fn) else {"c": {}, "f": {}}
        return inst, {int(k): v for k, v in old["c"].items()}, {int(k): v for k, v in old["f"].items()}


def _one(inst, start, end, raw):
    fn = os.path.join(raw, inst + ".json")
    old = json.load(open(fn)) if os.path.exists(fn) else {"c": {}, "f": {}}
    have = {int(k): v for k, v in old["c"].items()}
    fund = {int(k): v for k, v in old["f"].items()}
    if not have:
        have.update(candles(inst, start, end))
    else:
        lo, hi = min(have), max(have)
        if lo > start:
            have.update(candles(inst, start, lo - H))
        if hi < end:
            have.update(candles(inst, hi + H, end))
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
    ap.add_argument("--update", action="store_true", help="extend the existing panel at --out to the last closed hour")
    a = ap.parse_args()
    if a.update:
        return update(a)
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
    report()


def update(a):
    """Extend an existing panel: fetch each instrument after its last stored hour, add new listings."""
    z = np.load(a.out)
    P = {k: z[k] for k in z.files}
    old_insts = [str(x) for x in P["insts"]]
    start = int(P["ts"][0])
    end = int(time.time() * 1000) // H * H - H
    meta = [x for x in get("public/instruments?instType=SWAP", CANDLES) or []
            if x["instId"].endswith("-USDT-SWAP") and x["state"] == "live"]
    if not meta:
        sys.exit("instrument list unavailable")
    cat = {x["instId"]: x.get("instCategory") == "1" for x in meta}
    if "crypto" in P:
        old_cat = {s: bool(c) for s, c in zip(old_insts, P["crypto"])}
    else:
        side = os.path.splitext(a.out)[0] + ".noncrypto.json"
        non = set(json.load(open(side))) if os.path.exists(side) else set()
        old_cat = {s: s.split("-")[0] not in non for s in old_insts}
    insts = old_insts + sorted(set(cat) - set(old_insts))
    T0, T = len(P["ts"]), (end - start) // H + 1
    last = {}
    for j, s_ in enumerate(old_insts):
        ok = np.nonzero(np.isfinite(P["c"][:, j]))[0]
        last[s_] = start + int(ok[-1]) * H if len(ok) else None
    fund_last = {}
    for j, s_ in enumerate(old_insts):
        ok = np.nonzero(np.isfinite(P["fund"][:, j]))[0] if "fund" in P else []
        fund_last[s_] = start + int(ok[-1]) * H if len(ok) else start

    def one_upd(s_):
        if s_ not in cat:                       # delisted: keep what we have
            return s_, {}, {}
        try:
            frm = (last[s_] + H) if last.get(s_) else start
            bars = candles(s_, frm, end) if frm <= end else {}
            fr = {t: r for t, r in funding(s_, fund_last.get(s_, start)).items() if t > fund_last.get(s_, start)}
        except Incomplete:                      # keep the panel gap-free: retry this instrument next run
            print(f"{s_}: update incomplete, skipped (re-run to retry)", file=sys.stderr)
            return s_, {}, {}
        return s_, bars, fr

    print(f"update {len(insts)} instruments ({len(insts) - len(old_insts)} new) to "
          f"{dt.datetime.fromtimestamp(end / 1000, dt.timezone.utc):%Y-%m-%d %H:00}", file=sys.stderr)
    with ThreadPoolExecutor(a.workers) as ex:
        res = list(ex.map(one_upd, insts))
    N = len(insts)
    out = {}
    for k in ("o", "h", "l", "c", "q", "fund"):
        arr = np.full((T, N), np.nan, np.float32)
        if k in P:
            arr[:T0, :len(old_insts)] = P[k][:T]
        out[k] = arr
    for j, (s_, bars, fr) in enumerate(res):
        for t, v in bars.items():
            i = (t - start) // H
            if 0 <= i < T:
                for k, x in zip("ohlcq", v):
                    out[k][i, j] = x
        for t, r in fr.items():
            i = (t - start) // H
            if 0 <= i < T:
                out["fund"][i, j] = r
    crypto = np.array([cat.get(s_, old_cat.get(s_, True)) for s_ in insts])
    tmp = a.out + ".tmp.npz"
    np.savez_compressed(tmp, ts=np.arange(start, end + H, H, dtype=np.int64), insts=np.array(insts), crypto=crypto, **out)
    os.replace(tmp, a.out)
    print(f"wrote {a.out}: {T} hours × {N} instruments", file=sys.stderr)
    report()


def report():
    if FAILED:
        print(f"{len(FAILED)} requests failed after retries; affected instruments were left as they were", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
