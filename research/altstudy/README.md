# altstudy — research behind alt-regime-trend

Vectorised research engine and the experiments of [docs/research-alt-perps.md](../../docs/research-alt-perps.md)
(literature: [docs/research-alt-perps-literature.md](../../docs/research-alt-perps-literature.md)). The
production strategy is `strategies/library/alt_regime.py`; the offline backtest of the strategy file
itself is [`research/altbt`](../altbt/README.md). Use this directory to try new signal families
quickly (seconds per run over two years × 200 coins), then port what survives into the strategy and
confirm it with `research/altbt`.

## Data

All scripts read the hourly panel `data/altbt/panel.npz` written by `research/altbt/fetch.py`
(override with `ALTSTUDY_PANEL=/path/panel.npz`). Keep it current with
`python3 research/altbt/fetch.py --update`. The owner's copy lives on the server under
`/opt/ctg-research/repo/data/altbt/` and is updated weekly (see the research note).

## Running

Run from this directory (scripts `exec` each other by relative path and write their outputs here;
outputs are git-ignored). Needs numpy, pandas, scipy, lightgbm — or the image in `Dockerfile`:

```sh
docker build -t ctg-research -f research/altstudy/Dockerfile research/altstudy
docker run --rm --cpus 1 --memory 1500m -v $PWD:/w -w /w/research/altstudy ctg-research python3 combo.py
```

Periods used everywhere: Y1 = 2024-11-01 → 2025-10-01 (design), Y2 = 2025-10-01 → end of data.
Sizing: 125 USDT margin × 8x fixed notional, K = 5, loss cap 2 × margin (see `engine.Cfg`).

## Files

| File | What it is / question | Finding (2026-10) |
|---|---|---|
| `engine.py` | Panel loader, daily top-N universe (30-day turnover, ≥ 30 days listed, crypto only, no BTC/ETH), hourly multi-instrument simulator mirroring the Go backtest (`simulate`), summary `stats` | — |
| `features.py` | Vectorised [T, N] indicators: EMA/RMA, rolling max/min, 4H ATR on the hourly grid, breadth, cross-sectional ranks, rolling BTC beta and residual returns | — |
| `explore.py` | First pass: Donchian breakouts (with/without regime) and cross-sectional residual momentum/reversal | XS signals not robust; regime-gated breakouts promising |
| `explore2.py` | Breakout grid (`a`: lookback × gate × side; `b`: stops, retest entries, trails, holds, volume, K); `sig_for()` builds the signal arrays | Market entries + midline exit; retest entries and trails kill the runners |
| `explore3.py` | Profit locks (ATR trail after x R, break-even) | Rejected |
| `stall.py` | Stall exits (close if < x R after y h) | No consistent gain |
| `robust.py` | Plateau check: lookback 20–80 × ATR stop 2–4 × volume 1.2–2 | Positive in both years at every point |
| `univ_test.py` | Universe size 30/50/100/200 | Only the top 200 works |
| `events.py` → `meta.py` | 45 k breakout events with 20 features and simulated R; monthly walk-forward ridge / LightGBM meta-label | IC ≈ 0.07, no economic gain — runners are unpredictable |
| `shorts.py` | Weak-alt shorts by residual momentum, regime gates, holds | Breadth < 40 %, 72 h residual, 72 h hold: positive in both years |
| `combo.py` | The two legs combined, presets conservative / balanced / aggressive, monthly P&L; writes `combo_res.pkl` | Basis of the strategy |
| `mc.py` | Block bootstrap of daily P&L → one-year drawdown quantiles in margins (`dd95` used by the strategy's suggested margin) | dd95 ≈ 58 (conservative) / 51 (aggressive) margins |
| `sizing.py` | Fixed notional vs fixed risk per trade | Fixed notional kept |

## Adding an experiment

1. Build signal arrays `enter` (+1/−1 at a bar close), `score`, `stop` (fraction), optional `exit`,
   `leg`, `atr` as in `combo.build()`; run `engine.simulate(P, sig, engine.Cfg(...), t0, t1, U)` for Y1
   and Y2 and print `engine.stats`.
2. Judge on both years, the profit factor, `net_x5` (net without the best five trades) and the share
   of positive months — not on the best Y2 number. Prefer parameter plateaus.
3. Port survivors into the strategy, then run `research/altbt/run.py` on the strategy file and update
   the research note (results table, `dd95` if sizing changes).
