import sys, itertools
sys.argv=[sys.argv[0],'none']
exec(open('explore2.py').read())
for n, vthr in ((20, 1.2), (55, 1.5)):
    s = sig_for(n, vthr, "regime", "both", "atr3", "mid", "market")
    for tk, after in itertools.product((3, 4, 6), (2, 4, 8)):
        run(f"n{n} v{vthr} mid + trail{tk} after{after}R", s, max_hold=0, trail_k=tk, trail_after_R=after)
    for be in (1, 2):
        run(f"n{n} v{vthr} mid + BE{be}R", s, max_hold=0, be_after_R=be)
