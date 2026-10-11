"""First pass over signal families on the top-200 alt universe. Y1 = design, Y2 = last year (test)."""
import sys, time
import numpy as np
import engine as E
import features as Fe

t_start = time.time()
P = E.load_panel()
U = E.universe(P)
ts = P["ts"]
o, h, l, c, q = P["o"], P["h"], P["l"], P["c"], P["q"]
T, N = c.shape
Y1 = (int(np.searchsorted(ts, 1730419200000)), int(np.searchsorted(ts, 1759276800000)))  # 2024-11-01 .. 2025-10-01
Y2 = (Y1[1], T)                                                                         # 2025-10-01 .. now
bi = P["insts"].index("BTC-USDT-SWAP")
cb = c[:, bi]

# ---- shared features ----
atr4 = Fe.atr_tf(P, 4)                       # ATR of 4H bars (price units)
atr1 = Fe.atr(h, l, c, 14)
ema_b = Fe.ema(cb[:, None], 480)[:, 0]       # BTC 20-day EMA
btc_up = cb > ema_b
br = Fe.breadth(c, U, 168)                   # share of alts above their 7-day EMA
q24 = Fe.rsum(np.nan_to_num(q), 24)
q7d = Fe.rmean(q24, 168, 120)
volx = q24 / q7d
is4 = (ts // E.H) % 4 == 3                   # hour index closing a 4H bar
print(f"features {time.time() - t_start:.0f}s", flush=True)


def run(name, sig, cfg=None, **kw):
    cfg = cfg or E.Cfg(**kw)
    out = []
    for (a, b) in (Y1, Y2):
        tr, eq = E.simulate(P, sig, cfg, a, b, U)
        s = E.stats(tr, eq, ts[a:b], cfg)
        out.append(s)
    f = lambda s: (f"n={s['trades']:4d} net={s['net']:8.0f} dd={s['maxdd']:7.0f} win={s['win']:.2f} pf={s['pf']:.2f} "
                   f"R={s['avgR']:+.2f} x5={s['net_x5']:7.0f} m+={s['pos_months']:.2f} L={s['long_pnl']:7.0f} S={s['short_pnl']:7.0f}")
    print(f"{name:42s} | Y1 {f(out[0])} | Y2 {f(out[1])}", flush=True)
    return out


def mk(enter, score, stop, exit_=None, limit=None, at=None):
    return dict(enter=enter.astype(np.int8), score=np.nan_to_num(score, nan=-9).astype(np.float32),
                stop=stop.astype(np.float32), exit=exit_, limit=limit, atr=atr4 if at is None else at)


fam = sys.argv[1] if len(sys.argv) > 1 else "all"

if fam in ("all", "donch"):
    for n in (20, 55):                    # 4H bars
        hh = Fe.shift(Fe.rmax(h, 4 * n), 1)
        ll = Fe.shift(Fe.rmin(l, 4 * n), 1)
        up = (c > hh) & is4[:, None]
        dn = (c < ll) & is4[:, None]
        mid = (Fe.rmax(h, 2 * n) + Fe.rmin(l, 2 * n)) / 2
        for gate in ("none", "regime"):
            lg = up & (volx > 1.2)
            sg = dn & (volx > 1.2)
            if gate == "regime":
                lg &= (btc_up & (br > 0.5))[:, None]
                sg &= (~btc_up & (br < 0.5))[:, None]
            enter = lg.astype(int) - sg.astype(int)
            stop = 3 * atr4 / c
            ex = np.where(c < mid, 1, 0) * 0
            ex = np.where(c < mid, -1, np.where(c > mid, 1, 0))  # close long below mid, short above
            # exit encoding: exit value == -side means close
            sig = mk(enter, volx, stop, exit_=ex)
            run(f"donch{n} {gate} midexit", sig, max_hold=0, trail_k=0)
            run(f"donch{n} {gate} trail3", mk(enter, volx, stop), max_hold=0, trail_k=3)

if fam in ("all", "xs"):
    beta, resid = Fe.beta_resid(c, cb, 168)
    for hz in (24, 72, 168):
        rr = Fe.rsum(np.nan_to_num(resid), hz)
        rk = Fe.xs_rank(rr, U)
        for mode in ("mom", "rev"):
            sgn = 1 if mode == "mom" else -1
            top = (rk > 0.95) & is4[:, None]
            bot = (rk < 0.05) & is4[:, None]
            enter = sgn * (top.astype(int) - bot.astype(int))
            score = np.abs(rk - 0.5)
            stop = 3 * atr4 / c
            for hold in (24, 72):
                run(f"xs{hz} {mode} hold{hold}", mk(enter, score, stop), max_hold=hold)
            run(f"xs{hz} {mode} longonly h72", mk(np.maximum(enter, 0), score, stop), max_hold=72)
            run(f"xs{hz} {mode} shortonly h72", mk(np.minimum(enter, 0), score, stop), max_hold=72)
