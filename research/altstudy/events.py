"""Breakout events (4H closes, both sides, lookbacks 20/55 4H bars) with features at decision time and
an independently simulated outcome (entry next open, 3×ATR4 stop capped at 25 %, Donchian-mid exit).
Writes events.npz. Used for meta-labelling / walk-forward models."""
import numpy as np
import engine as E
import features as Fe

P = E.load_panel()
U = E.universe(P)
ts = P["ts"]
o, h, l, c, q, fund = P["o"], P["h"], P["l"], P["c"], P["q"], P["fund"]
T, N = c.shape
bi = P["insts"].index("BTC-USDT-SWAP")
cb = c[:, bi]
atr4 = Fe.atr_tf(P, 4)
ema_b = Fe.ema(cb[:, None], 480)[:, 0]
br = Fe.breadth(c, U, 168)
br30 = Fe.breadth(c, U, 720)
with np.errstate(all="ignore"):
    q24 = Fe.rsum(np.nan_to_num(q), 24)
    volx = q24 / Fe.rmean(q24, 168, 120)
    q30 = Fe.rmean(q24, 720, 360)
is4 = (ts // E.H) % 4 == 3
first = np.argmax(np.isfinite(c), 0)
age = np.arange(T)[:, None] - first[None, :]
atr_rel = atr4 / Fe.rmean(atr4, 720, 360)
r24, r168, r720 = Fe.ret(c, 24), Fe.ret(c, 168), Fe.ret(c, 720)
_, resid = Fe.beta_resid(c, cb, 168)
rr168 = Fe.rsum(np.nan_to_num(resid), 168)
hi90, lo90 = Fe.rmax(h, 2160), Fe.rmin(l, 2160)
fund_last = np.copy(fund)
fund_last = Fe.df(fund_last).ffill(limit=16).to_numpy(np.float32)
mkt24 = Fe.xs_mean(r24, U)
btc24 = Fe.ret(cb[:, None], 24)[:, 0]
qrank = Fe.xs_rank(np.log(q30 + 1), U)

rows = []
for n in (20, 55):
    hh = Fe.shift(Fe.rmax(h, 4 * n), 1)
    ll = Fe.shift(Fe.rmin(l, 4 * n), 1)
    mid = (Fe.rmax(h, 2 * n) + Fe.rmin(l, 2 * n)) / 2
    up = (c > hh) & is4[:, None] & U
    dn = (c < ll) & is4[:, None] & U
    for side, m in ((1, up), (-1, dn)):
        ti, ji = np.nonzero(m)
        for t, j in zip(ti, ji):
            if t + 2 >= T or not np.isfinite(atr4[t, j]) or not np.isfinite(o[t + 1, j]):
                continue
            entry = o[t + 1, j]
            sf = min(max(3 * atr4[t, j] / c[t, j], 0.01), 0.25)
            stop = entry * (1 - side * sf)
            e = min(T, t + 1 + 1500)
            ll_ = l[t + 1:e, j]
            hh_ = h[t + 1:e, j]
            hit = (ll_ <= stop) if side > 0 else (hh_ >= stop)
            k_stop = np.argmax(hit) if hit.any() else 10**9
            cc = c[t + 1:e, j]
            mm = mid[t + 1:e, j]
            ex = (cc < mm) if side > 0 else (cc > mm)
            k_mid = np.argmax(ex) if ex.any() else 10**9
            if k_stop == 10**9 and k_mid == 10**9:
                continue                     # still open at the end of data
            if k_stop <= k_mid:
                px = min(stop, o[t + 1 + k_stop, j]) if side > 0 else max(stop, o[t + 1 + k_stop, j])
                k = k_stop
            else:
                k = k_mid + 1
                if t + 1 + k >= T:
                    continue
                px = o[t + 1 + k, j]
            ret = side * (px / entry - 1) - 0.0012     # fees + slippage round trip
            # mfe within the trade
            seg_h, seg_l = hh_[:k + 1], ll_[:k + 1]
            mfe = (np.nanmax(seg_h) / entry - 1) if side > 0 else (1 - np.nanmin(seg_l) / entry)
            s = side
            rows.append((t, j, side, n, t + 1 + k, ret, ret / sf, sf, mfe,
                         s * (c[t, j] - (hh[t, j] if s > 0 else ll[t, j])) / atr4[t, j],
                         np.log(volx[t, j]) if volx[t, j] > 0 else np.nan,
                         atr4[t, j] / c[t, j], atr_rel[t, j],
                         s * r24[t, j], s * r168[t, j], s * r720[t, j], s * rr168[t, j],
                         s * (br[t] - 0.5), s * (br30[t] - 0.5), s * (cb[t] / ema_b[t] - 1),
                         qrank[t, j], np.log1p(age[t, j] / 24),
                         (c[t, j] / hi90[t, j] - 1) if s > 0 else (c[t, j] / lo90[t, j] - 1),
                         s * np.nan_to_num(fund_last[t, j]) * 1e4,
                         (hh[t, j] - ll[t, j]) / atr4[t, j],
                         s * mkt24[t], s * btc24[t]))

cols = ["t", "j", "side", "n", "t_out", "ret", "R", "stopf", "mfe",
        "brk_atr", "lvolx", "atrp", "atr_rel", "r24", "r168", "r720", "rr168", "br7", "br30", "btc_tr",
        "qrank", "lage", "dist90", "fund_bp", "range_atr", "mkt24", "btc24"]
A = np.array(rows, dtype=np.float64)
np.savez_compressed("events.npz", A=A, cols=np.array(cols))
print(A.shape, "events;", "mean R", np.nanmean(A[:, 6]), "median R", np.nanmedian(A[:, 6]))
