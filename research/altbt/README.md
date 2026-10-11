# altbt — offline multi-coin backtest

Runs a strategy file from `strategies/` through the real SDK runtime over an hourly OKX panel, for
fast parameter sweeps and spans beyond what production stores (it keeps 400 days of 1H candles; a
backtest account covers at most 365 days). Used for `alt-regime-trend`
(research: [docs/research-alt-perps.md](../../docs/research-alt-perps.md)).

```sh
# 1. data (public OKX endpoints; ~1–2 h for two years of every live swap at the rate limit;
#    re-runs only fetch what is missing)
python3 research/altbt/fetch.py --from 2024-10-01                 # → data/altbt/panel.npz

# 2. backtest (numpy only; a 2-year, 200-coin, 4H run takes ~2.5 min on one core)
python3 research/altbt/run.py strategies/library/alt_regime.py \
    --params '{"style": "aggressive", "margin_usdt": 125}' \
    --deposit 3000 --from 2025-10-01 --to 2026-10-11 --out data/altbt/out/a125
```

`--params` takes the strategy's parameter names with human values (choice names, booleans,
numbers). `--limits` overrides the account limits in the app's camelCase form; the defaults are
`maxPositions 5, maxLeverage 8, riskPct 0.1, maxInstPct 0.3, dailyLossPct 0 (off), cooldownMin 0,
takerFeeBp 5`. Output: a JSON summary (net, max drawdown, profit factor, net without the best five
trades, monthly P&L, exits, rejected signals), plus `trades.csv` and `equity.csv` with `--out`.

On a small machine run it in a capped container, e.g. with the strategy image:
`docker run --rm --cpus 1 --memory 1200m --network none -v $PWD:/w -w /w --entrypoint python3 ctg-strategy:local research/altbt/run.py …`.

`starts.py trades.csv …` measures start-date risk from a run's trades: for every start day, the
trades opened in the next 365 days, the deepest dip below the start and the one-year P&L.

## Model

Mirrors the Go backtest (`internal/trade`, see `harness.py` docstring): 1H base bars, signals fill at
the next hour's open plus half spread, stop before target within a bar, gaps fill at the open,
isolated liquidation, sizing from `risk.go` (riskPct × strength ÷ stop distance, leverage lowered so
the liquidation stays beyond the stop, margin capped by cash and maxInstPct), time limits, funding.
`Screener(scope="market")` is rebuilt from the panel's 12h turnover. Not modelled: order-book depth,
lot sizes, maintenance-margin tiers, AI review and confirmations, adding to positions.

Known biases: instruments delisted before the download are missing (survivorship); OKX serves only
~3 months of funding history, earlier hours use `--funding-default` (0.01 % per 8h, longs pay).

## Server setup (owner's research box)

`ops/` holds the two scripts installed under `/opt/ctg-research/` on the server, next to a copy of
this repository in `/opt/ctg-research/repo` (code synced by hand; data in `repo/data/altbt/`):

- `bt.sh <script> …` — runs a Python script of the repo in a throwaway container (strategy image,
  1 CPU, 1.2 GB, no network, `nice`), so research never competes with production.
- `update-data.sh` — weekly cron (`13 4 * * 0`) extending the panel with `fetch.py --update` in the
  `ctg-research` image (`research/altstudy/Dockerfile`); log in `/opt/ctg-research/update-data.log`.
