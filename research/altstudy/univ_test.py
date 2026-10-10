import sys
sys.argv=[sys.argv[0],'none']
exec(open('explore2.py').read())
U200 = U
for top in (30, 50, 100, 200):
    U = E.universe(P, top=top)
    br = Fe.breadth(c, U200, 168)   # regime always measured on the broad market
    for n, v in ((20, 1.2), (55, 1.5)):
        run(f"top{top} n{n} v{v}", sig_for(n, v, "regime", "both", "atr3", "mid", "market"), max_hold=0)
