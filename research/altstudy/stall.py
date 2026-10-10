import sys, itertools
sys.argv=[sys.argv[0],'none']
exec(open('explore2.py').read())
for n, v in ((20, 1.2), (40, 1.5), (55, 1.5)):
    s = sig_for(n, v, "regime", "both", "atr3", "mid", "market")
    for sh, sr in itertools.product((12, 24, 48, 96), (0.5, 1.0, 2.0)):
        run(f"n{n} v{v} stall{sh}h<{sr}R", s, max_hold=0, stall_h=sh, stall_R=sr)
