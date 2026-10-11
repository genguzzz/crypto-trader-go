"""Combined strategy: leg 1 regime-gated Donchian breakout (trend), leg 2 weak-alt shorts in a broad
alt downtrend. Presets conservative / balanced / aggressive. Reports Y1, Y2, full, monthly."""
import sys
import numpy as np
import engine as E
import features as Fe

P = E.load_panel()
U = E.universe(P)
ts = P["ts"]
o, h, l, c, q = P["o"], P["h"], P["l"], P["c"], P["q"]
T, N = c.shape
Y0 = int(np.searchsorted(ts, 1730419200000))
Y1 = (Y0, int(np.searchsorted(ts, 1759276800000)))
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
_, resid = Fe.beta_resid(c, cb, 168)
RK = {}

PRESETS = {
    "conservative": dict(n=55, vthr=1.5, k=3, long_br=0.5, brk_short=False, xs=True, xs_br=0.4, xs_hz=72, xs_pct=0.05,
                         xs_hold=72, K=5),
    "balanced":     dict(n=40, vthr=1.5, k=3, long_br=0.5, brk_short=False, xs=True, xs_br=0.4, xs_hz=72, xs_pct=0.05,
                         xs_hold=72, K=5),
    "aggressive":   dict(n=20, vthr=1.2, k=3, long_br=0.5, brk_short=True, xs=True, xs_br=0.4, xs_hz=72, xs_pct=0.05,
                         xs_hold=72, K=5),
}


def build(p):
    n = p["n"]
    hh = Fe.shift(Fe.rmax(h, 4 * n), 1)
    ll = Fe.shift(Fe.rmin(l, 4 * n), 1)
    mid = (Fe.rmax(h, 2 * n) + Fe.rmin(l, 2 * n)) / 2
    up = (c > hh) & is4[:, None] & (volx > p["vthr"]) & (btc_up & (br > p["long_br"]))[:, None]
    dn = (c < ll) & is4[:, None] & (volx > p["vthr"]) & (~btc_up & (br < 0.5))[:, None] & p["brk_short"]
    enter = up.astype(np.int8) - dn.astype(np.int8)
    leg = np.where(enter != 0, 1, 0).astype(np.int8)
    score = np.where(enter != 0, 10 + np.nan_to_num(volx), -9)
    if p["xs"]:
        hz = p["xs_hz"]
        if hz not in RK:
            RK[hz] = Fe.xs_rank(Fe.rsum(np.nan_to_num(resid), hz), U)
        rk = RK[hz]
        xs = (rk < p["xs_pct"]) & is4[:, None] & (br < p["xs_br"])[:, None] & (enter == 0)
        enter = np.where(xs, -1, enter).astype(np.int8)
        leg = np.where(xs, 2, leg).astype(np.int8)
        score = np.where(xs, 1 - np.nan_to_num(rk, nan=1), score)
    ex = np.where(c < mid, -1, np.where(c > mid, 1, 0)).astype(np.int8)
    return dict(enter=enter, leg=leg, score=score.astype(np.float32), stop=(p["k"] * atr4 / c).astype(np.float32),
                exit=ex, atr=atr4)


def cfg_for(p, **kw):
    return E.Cfg(K=p["K"], max_hold=0, leg_hold={2: p["xs_hold"]}, exit_legs=(1,), **kw)


def report(name, sig, cfg):
    res = {}
    for lab, (a, b) in (("Y1", Y1), ("Y2", Y2), ("ALL", (Y0, T))):
        tr, eq = E.simulate(P, sig, cfg, a, b, U)
        res[lab] = (tr, eq, E.stats(tr, eq, ts[a:b], cfg))
    for lab in ("Y1", "Y2", "ALL"):
        s = res[lab][2]
        legs = {g: sum(t["pnl"] for t in res[lab][0] if t["leg"] == g) for g in (1, 2)}
        print(f"{name:13s} {lab:3s} n={s['trades']:4d} net={s['net']:7.0f} ({s['net_margins']:6.1f}M) x5={s['net_x5']:7.0f} "
              f"dd={s['maxdd']:7.0f} ({s['maxdd_margins']:6.1f}M) pf={s['pf']:.2f} win={s['win']:.2f} m+={s['pos_months']:.2f} "
              f"sharpe={s['sharpe']:.2f} trend={legs[1]:7.0f} short={legs[2]:6.0f} fees={s['fees']:.0f} fund={s['funding']:.0f}")
    return res


if __name__ == "__main__":
    import pickle
    out = {}
    for name, p in PRESETS.items():
        sig = build(p)
        res = report(name, sig, cfg_for(p))
        tr, eq, _ = res["ALL"]
        # monthly P&L (calendar months, UTC)
        mts = ts[Y0:T]
        mon = (mts // 1000 // 86400)
        import datetime as dt
        keys = [dt.datetime.utcfromtimestamp(x / 1000).strftime("%y%m") for x in mts]
        last = {}
        for k_, v in zip(keys, eq):
            last[k_] = v
        prev, line = 0.0, []
        for k_ in sorted(last):
            line.append(f"{k_}:{last[k_] - prev:+.0f}")
            prev = last[k_]
        print("   monthly", " ".join(line))
        out[name] = dict(trades=tr, eq=eq, p=p)
    pickle.dump(out, open("combo_res.pkl", "wb"))
