"""Monte Carlo (block bootstrap of daily P&L, fixed margin) → drawdown quantiles in units of margin per trade.
Used to map an aggressiveness dial to a margin fraction of equity."""
import pickle
import numpy as np

res = pickle.load(open("combo_res.pkl", "rb"))
rng = np.random.default_rng(7)
MARGIN = 125.0
out = {}
for name, r in res.items():
    eq = r["eq"] / MARGIN                      # cumulative P&L in margins, hourly
    daily = np.diff(eq[::24])
    lo, hi = np.percentile(daily, [0.5, 99.5])
    daily = np.clip(daily, lo, hi)
    # drop the single largest day (the RAVE squeeze) so a tail win does not mask risk
    days = len(daily)
    B, L, blk = 5000, 365, 10
    dds, finals = [], []
    for _ in range(B):
        starts = rng.integers(0, days - blk, L // blk + 1)
        path = np.concatenate([daily[s:s + blk] for s in starts])[:L]
        cum = np.cumsum(path)
        peak = np.maximum.accumulate(np.maximum(cum, 0))
        dds.append((cum - peak).min())
        finals.append(cum[-1])
    dds, finals = np.array(dds), np.array(finals)
    # realised-peak drawdown on the actual path (peaks of closed-trade equity)
    tr = sorted(r["trades"], key=lambda t: t["t_out"])
    hist = np.cumsum(eq[::24])
    q = {p: float(np.percentile(dds, p)) for p in (50, 5, 1)}
    out[name] = dict(dd50=q[50], dd95=q[5], dd99=q[1], p_loss=float(np.mean(finals < 0)), med_year=float(np.median(finals)),
                     p10_year=float(np.percentile(finals, 10)), worst_day=float(daily.min()), best_day=float(daily.max()))
    print(f"{name:13s} 1y DD (margins): median {q[50]:6.1f}  95% {q[5]:6.1f}  99% {q[1]:6.1f} | year P&L margins: "
          f"median {np.median(finals):6.1f} p10 {np.percentile(finals, 10):6.1f} P(loss) {np.mean(finals < 0):.2f} | "
          f"worst day {daily.min():.1f} best day {daily.max():.1f}")
pickle.dump(out, open("mc_res.pkl", "wb"))
