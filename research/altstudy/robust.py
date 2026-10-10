import sys, itertools
sys.argv=[sys.argv[0],'none']
exec(open('explore2.py').read())
def sig2(n, vthr, k):
    s = sig_for(n, vthr, "regime", "both", "atr3", "mid", "market")
    s["stop"] = (k * atr4 / c).astype(np.float32)
    return s
for n, k, vthr in itertools.product((20, 30, 40, 55, 80), (2, 3, 4), (1.2, 1.5, 2.0)):
    run(f"n{n} k{k} v{vthr}", sig2(n, vthr, k), max_hold=0)
import pickle; pickle.dump(rows, open('robust_rows.pkl','wb'))
