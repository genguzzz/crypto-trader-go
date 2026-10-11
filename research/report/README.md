# report — the owner's research report (Chinese, interactive)

`docs/research/alt-perps-report.html` is the owner-facing report of the altcoin strategy research:
every plan so far (pump shorting, BTC/ETH trend, ctg pool scores, alt-regime-trend), the current
strategy, backtest results, risk, a position-size calculator, the experiment log and the changelog.
It is a single self-contained file (no external scripts or fonts): open it in a browser, or from
GitHub via "Download raw file". Written in Chinese for the owner (exception in AGENTS.md); the
engineering record stays in English in `docs/research-alt-perps.md`.

| File | What |
|---|---|
| `history.json` | Narrative: summary, one entry per study (question, method, findings, verdict, lesson), strategy description, experiments, universe-size and ML results, Monte Carlo table, limits, next steps, changelog |
| `results/alt-regime.json` | Backtest data written by `collect.py` from `research/altbt` run outputs |
| `results/robust-grid.json` | Parameter plateau (lookback × ATR stop × volume) from `research/altstudy/robust.py` |
| `template.html` | Page and charts (plain SVG + JS, light/dark, responsive) |
| `build.py` | Embeds the JSON into the template → `docs/research/alt-perps-report.html` |

## Updating after new research

1. Run the experiments (`research/altstudy`) and the strategy backtests (`research/altbt`, on the
   server: `/opt/ctg-research/bt.sh research/altbt/run.py … --out out/<name>`).
2. Refresh the backtest data (on the server, then copy the file back):
   ```sh
   /opt/ctg-research/bt.sh research/report/collect.py out/<a>:<label> out/<b>:<label> … \
       > repo/research/report/results/alt-regime.json
   ```
   The first two scenarios are the headline ones (fixed 125 USDT margin, conservative then
   aggressive); they feed the summary tiles, concentration, legs and start-date charts.
3. Edit `history.json`: add a `studies` entry for a new plan (keep the old ones — the report is
   the history), update `summary`, `experiments`, `limits`, `next`, and append to `changelog`.
4. `python3 research/report/build.py`, open the HTML (desktop and phone width, light and dark),
   commit `history.json`, `results/` and the rebuilt HTML together.
