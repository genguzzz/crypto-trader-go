"""Walk-forward meta-label models on breakout events: monthly refit on events closed before the month."""
import numpy as np
import lightgbm as lgb
from scipy.stats import spearmanr

z = np.load("events.npz")
A, cols = z["A"], [str(x) for x in z["cols"]]
ci = {k: i for i, k in enumerate(cols)}
FEATS = ["side", "n", "brk_atr", "lvolx", "atrp", "atr_rel", "r24", "r168", "r720", "rr168", "br7", "br30",
         "btc_tr", "qrank", "lage", "dist90", "fund_bp", "range_atr", "mkt24", "btc24"]
import engine
P_ts = engine.load_panel()["ts"]
X = A[:, [ci[f] for f in FEATS]]
X = np.where(np.isfinite(X), X, np.nan)
R = A[:, ci["R"]]
Rc = np.clip(R, -1.2, 5.0)
t, t_out = A[:, ci["t"]].astype(int), A[:, ci["t_out"]].astype(int)
H = 3600_000
month = ((P_ts[t] // 1000 // 86400 - 20089) // 30.44).astype(int)   # months since 2025-01-01 (approx)


class Ridge:
    """Ridge on winsorised, train-standardised features; NaN -> 0 after standardising."""

    def __init__(self, lam=10.0):
        self.lam = lam

    def fit(self, X, y):
        self.lo, self.hi = np.nanpercentile(X, 1, 0), np.nanpercentile(X, 99, 0)
        Z = np.clip(X, self.lo, self.hi)
        self.mu, self.sd = np.nanmean(Z, 0), np.nanstd(Z, 0) + 1e-9
        Z = np.nan_to_num((Z - self.mu) / self.sd)
        k = Z.shape[1]
        self.b0 = y.mean()
        self.w = np.linalg.solve(Z.T @ Z + self.lam * len(y) / 100 * np.eye(k), Z.T @ (y - self.b0))
        return self

    def predict(self, X):
        Z = np.nan_to_num((np.clip(X, self.lo, self.hi) - self.mu) / self.sd)
        return self.b0 + Z @ self.w


def walk(model_fn, start_month=4):
    pred = np.full(len(R), np.nan)
    for m in range(start_month, month.max() + 1):
        test = month == m
        if not test.any():
            continue
        m_start = t[test].min()
        train = t_out < m_start
        mdl = model_fn().fit(X[train], Rc[train])
        pred[test] = mdl.predict(X[test])
    return pred


class GBM:
    def fit(self, X, y):
        self.m = lgb.LGBMRegressor(n_estimators=300, learning_rate=0.03, num_leaves=15, min_child_samples=200,
                                   subsample=0.8, subsample_freq=1, colsample_bytree=0.8, verbose=-1)
        self.m.fit(X, y)
        return self

    def predict(self, X):
        return self.m.predict(X)


if __name__ == "__main__":
    for name, fn in (("ridge", lambda: Ridge(10)), ("gbm", GBM)):
        p = walk(fn)
        ok = np.isfinite(p)
        print(f"== {name}: events scored {ok.sum()}")
        ics = []
        for m in np.unique(month[ok]):
            s = ok & (month == m)
            ic = spearmanr(p[s], Rc[s]).correlation
            ics.append(ic)
        ics = np.array(ics)
        print(f"monthly IC mean {ics.mean():+.3f}  pos {np.mean(ics > 0):.2f}  t {ics.mean() / ics.std() * np.sqrt(len(ics)):.2f}")
        for lo_, hi_ in ((0, 0.5), (0.5, 0.8), (0.8, 0.9), (0.9, 1.0)):
            qs = np.nanquantile(p[ok], [lo_, hi_])
            s = ok & (p >= qs[0]) & (p <= qs[1])
            print(f"  pred q{lo_:.1f}-{hi_:.1f}: n={s.sum():5d} meanR={R[s].mean():+.3f} medR={np.median(R[s]):+.3f} "
                  f"clipR={Rc[s].mean():+.3f} win={np.mean(R[s] > 0):.2f}")
        np.save(f"pred_{name}.npy", p)
        if name == "ridge":
            mdl = Ridge(10).fit(X[ok | ~ok], Rc)
            for f, w in sorted(zip(FEATS, mdl.w), key=lambda x: -abs(x[1])):
                print(f"   {f:10s} {w:+.4f}")
