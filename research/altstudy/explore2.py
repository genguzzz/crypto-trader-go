"""Breakout-trend family grid: entry, stop, exit, regime. Prints Y1 (design) and Y2 (last year)."""
import itertools, sys, time
import numpy as np
import engine as E
import features as Fe

P = E.load_panel()
U = E.universe(P)
ts = P["ts"]
o, h, l, c, q = P["o"], P["h"], P["l"], P["c"], P["q"]
T, N = c.shape
Y1 = (int(np.searchsorted(ts, 1730419200000)), int(np.searchsorted(ts, 1759276800000)))
Y2 = (Y1[1], T)
bi = P["insts"].index("BTC-USDT-SWAP")
cb = c[:, bi]
atr4 = Fe.atr_tf(P, 4)
btc_up = cb > Fe.ema(cb[:, None], 480)[:, 0]
br = Fe.breadth(c, U, 168)
with np.errstate(all="ignore"):
    q24 = Fe.rsum(np.nan_to_num(q), 24)
    volx = q24 / Fe.rmean(q24, 168, 120)
is4 = (ts // E.H) % 4 == 3
rows = []
print(f"{'name':34s} | Y1: n net x5 dd pf m+ L S | Y2: n net x5 dd pf m+ L S")


def run(name, sig, **kw):
    cfg = E.Cfg(**kw)
    out = []
    for (a, b) in (Y1, Y2):
        tr, eq = E.simulate(P, sig, cfg, a, b, U)
        out.append(E.stats(tr, eq, ts[a:b], cfg))
    f = lambda s: f"{s['trades']:4d} {s['net']:7.0f} {s['net_x5']:7.0f} {s['maxdd']:7.0f} {s['pf']:4.2f} {s['pos_months']:.2f} {s['long_pnl']:6.0f} {s['short_pnl']:6.0f}"
    print(f"{name:34s} | {f(out[0])} | {f(out[1])}", flush=True)
    rows.append((name, out))
    return out


def sig_for(n, vthr, gate, side, stop_kind, exit_kind, entry_kind):
    hh = Fe.shift(Fe.rmax(h, 4 * n), 1)
    ll = Fe.shift(Fe.rmin(l, 4 * n), 1)
    up = (c > hh) & is4[:, None] & (volx > vthr)
    dn = (c < ll) & is4[:, None] & (volx > vthr)
    if gate == "regime":
        up &= (btc_up & (br > 0.5))[:, None]
        dn &= (~btc_up & (br < 0.5))[:, None]
    elif gate == "breadth":
        up &= (br > 0.55)[:, None]
        dn &= (br < 0.35)[:, None]
    elif gate == "btc":
        up &= btc_up[:, None]
        dn &= ~btc_up[:, None]
    if side == "long":
        dn &= False
    elif side == "short":
        up &= False
    enter = up.astype(np.int8) - dn.astype(np.int8)
    if stop_kind == "atr3":
        stop = 3 * atr4 / c
    elif stop_kind == "atr2":
        stop = 2 * atr4 / c
    else:  # swing: the breakout level's opposite side over n/2 bars, at least 1.5 ATR
        sw_l = Fe.rmin(l, 2 * n)
        sw_h = Fe.rmax(h, 2 * n)
        stop = np.where(enter > 0, (c - sw_l) / c, (sw_h - c) / c)
        stop = np.fmax(stop, 1.5 * atr4 / c)
    ex = None
    if exit_kind == "mid":
        mid = (Fe.rmax(h, 2 * n) + Fe.rmin(l, 2 * n)) / 2
        ex = np.where(c < mid, -1, np.where(c > mid, 1, 0)).astype(np.int8)
    limit = None
    if entry_kind == "retest":   # rest a limit at the broken level (+0.5 ATR) for a few hours
        limit = np.where(enter > 0, hh + 0.5 * atr4, np.where(enter < 0, ll - 0.5 * atr4, np.nan))
    return dict(enter=enter, score=np.nan_to_num(volx, nan=-9).astype(np.float32), stop=stop.astype(np.float32),
                exit=ex, limit=limit, atr=atr4)


grid = sys.argv[1] if len(sys.argv) > 1 else "a"
if grid == "a":
    for n, gate, side in itertools.product((20, 40, 55), ("regime", "breadth", "btc"), ("both", "long")):
        s = sig_for(n, 1.2, gate, side, "atr3", "mid", "market")
        run(f"n{n} {gate} {side} atr3 mid", s, max_hold=0)
elif grid == "b":
    for n in (20, 55):
        for stop_kind, entry_kind in itertools.product(("atr2", "atr3", "swing"), ("market", "retest")):
            s = sig_for(n, 1.2, "regime", "both", stop_kind, "mid", entry_kind)
            run(f"n{n} regime {stop_kind} mid {entry_kind}", s, max_hold=0, limit_wait=8)
        s = sig_for(n, 1.2, "regime", "both", "atr3", "none", "market")
        for tk in (4, 6, 8):
            run(f"n{n} regime atr3 trail{tk}", s, max_hold=0, trail_k=tk)
        for hold in (72, 168, 336):
            s = sig_for(n, 1.2, "regime", "both", "atr3", "mid", "market")
            run(f"n{n} regime atr3 mid hold{hold}", s, max_hold=hold)
        for vthr in (0, 1.0, 1.5, 2.0):
            s = sig_for(n, vthr, "regime", "both", "atr3", "mid", "market")
            run(f"n{n} regime atr3 mid volx>{vthr}", s, max_hold=0)
        for K in (3, 5, 8):
            s = sig_for(n, 1.2, "regime", "both", "atr3", "mid", "market")
            run(f"n{n} regime atr3 mid K{K}", s, max_hold=0, K=K)
