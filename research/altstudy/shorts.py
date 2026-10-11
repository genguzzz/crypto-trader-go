import sys, itertools
sys.argv=[sys.argv[0],'none']
exec(open('explore2.py').read())
beta, resid = Fe.beta_resid(c, cb, 168)
bear = (~btc_up & (br < 0.5))
bear2 = (br < 0.4)
for hz in (72, 168):
    rk = Fe.xs_rank(Fe.rsum(np.nan_to_num(resid), hz), U)
    for gate_name, g in (("none", np.ones(T, bool)), ("bear", bear), ("br<.4", bear2)):
        for pct in (0.05, 0.10):
            bot = (rk < pct) & is4[:, None] & g[:, None]
            s = dict(enter=-bot.astype(np.int8), score=(1 - np.nan_to_num(rk, nan=1)).astype(np.float32),
                     stop=(3 * atr4 / c).astype(np.float32), atr=atr4)
            for hold in (24, 72):
                run(f"short xs{hz} {gate_name} p{pct} h{hold}", s, max_hold=hold)
