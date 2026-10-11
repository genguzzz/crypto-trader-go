import sys
sys.argv=[sys.argv[0]]
exec(open('combo.py').read().split('if __name__')[0])
for name in ("conservative", "aggressive"):
    p = PRESETS[name]; sig = build(p)
    for mode, kw in (("notional", {}), ("risk1M", dict(size_mode="risk", risk=125.0)), ("risk0.5M", dict(size_mode="risk", risk=62.5))):
        cfg = cfg_for(p, **kw)
        for lab, (a, b) in (("Y1", Y1), ("Y2", Y2)):
            tr, eq = E.simulate(P, sig, cfg, a, b, U)
            s = E.stats(tr, eq, ts[a:b], cfg)
            avg_not = np.mean([t["notional"] for t in tr])
            print(f"{name:12s} {mode:9s} {lab} net={s['net']:7.0f} x5={s['net_x5']:7.0f} dd={s['maxdd']:7.0f} ret/dd={-s['net']/s['maxdd']:.2f} sharpe={s['sharpe']:.2f} avgNotional={avg_not:.0f}")
