"""Start-date risk: for every start day, trades opened on/after it, realised P&L over the next 365 days."""
import csv, sys
import numpy as np
D = 86400_000
for path in sys.argv[1:]:
    tr = list(csv.DictReader(open(path)))
    t_in = np.array([int(t["t_in"]) for t in tr]); t_out = np.array([int(t["t_out"]) for t in tr])
    pnl = np.array([float(t["pnl"]) for t in tr])
    m = float(np.median([float(t["margin"]) for t in tr])) if tr else 1
    first, last = t_in.min(), t_out.max()
    worst, finals, ruin = [], [], {}
    for s in range(int(first), int(last - 365 * D), D):
        sel = (t_in >= s) & (t_in < s + 365 * D)
        o = np.argsort(t_out[sel]); cum = np.cumsum(pnl[sel][o])
        worst.append(min(0.0, cum.min()) if len(cum) else 0.0); finals.append(cum[-1] if len(cum) else 0.0)
    worst, finals = np.array(worst), np.array(finals)
    print(path.split("/")[-2], f"starts={len(worst)}  worst dip from start: min {worst.min():.0f}  p5 {np.percentile(worst,5):.0f}  median {np.median(worst):.0f}",
          f"| 1y P&L: p5 {np.percentile(finals,5):.0f}  p25 {np.percentile(finals,25):.0f}  median {np.median(finals):.0f}  P(loss) {np.mean(finals<0):.2f}",
          "| P(dip below -X):", " ".join(f"{x}:{np.mean(worst < -x):.2f}" for x in (1500, 3000, 6000, 10000)))
