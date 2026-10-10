# Research: systematic strategies for altcoin perpetuals

Issue [#8](https://github.com/genguzzz/crypto-trader-go/issues/8). Outcome: the built-in strategy
`alt-regime-trend` (`strategies/library/alt_regime.py`) and the offline backtest `research/altbt/`.
Research scripts and intermediate data live outside the repository (owner's workspace, `alt-study/`).

## Question

The owner trades OKX USDT perps on altcoins by hand: 100–150 USDT margin per trade at 8x, a stop of
about 200 USDT, at most five positions. Can a rule set, built from published evidence and tested on
the ~200 most-traded altcoins, decide entries and dynamic stops (hard cap: a loss of 200 % of the
margin), size positions from the account equity with one aggressiveness dial, and be expected to
make money over the last year?

## Data and method

- **Universe**: every live OKX USDT swap with instCategory 1 (crypto; stock, index and commodity
  swaps excluded), hourly candles 2024-10-01 → 2026-10-10 from the public API. Each day the top 200
  by trailing 30-day USDT turnover, listed ≥ 30 days, excluding BTC and ETH.
- **Split**: Y1 = 2024-11 → 2025-09 (design), Y2 = 2025-10 → 2026-10 (the "last year"). Parameters
  were chosen on plateaus that hold in both years, not on the best Y2 point.
- **Execution model** (same as the Go backtest): decision at a bar close, market fill at the next
  hour's open with 5 bp slippage and 5 bp taker fee; stop before target inside a bar, gaps fill at
  the open; funding from settled rates where known.
- **Sizing** (owner's scheme): fixed notional = margin × leverage (125 × 8 = 1000 USDT), stop distance
  from the price action, capped so the loss at the stop is ≤ 2 × margin (25 % at 8x). Results are
  quoted in USDT at that size and in "margins" (multiples of the per-trade margin), which scale
  linearly with any other margin.
- **Biases**: instruments delisted before the download are missing (survivorship: crashed coins
  that longs would have lost on, and shorts would have won on, are absent). OKX serves ~3 months of
  funding history; earlier funding is assumed (0.01 % per 8 h, longs pay).

## Literature (verified sources; full notes in the owner's workspace)

| Source | Finding we use |
|---|---|
| Liu & Tsyvinski, *RFS* 2021; Liu, Tsyvinski & Wu, *JF* 2022 | Crypto returns load on market, size and momentum factors; time-series momentum is strong |
| Zarattini, Pagani & Barbon 2025, SSRN 5209907 | Donchian-ensemble trend on the top coins, Sharpe > 1.5 at low leverage; works on BTC/ETH far better than on alts |
| Zaremba et al., *IRFA* 2021; Grobys et al., *FMPM* 2025 | Short-term reversal in illiquid coins, momentum in large ones; momentum crashes after panics |
| Daniel & Moskowitz, *JFE* 2016 | Momentum crashes are forecastable from the market state → regime gate |
| Moreira & Muir, *JF* 2017 | Volatility-managed exposure; tested as fixed-risk sizing (no gain here) |
| Kaminski & Lo, *JFM* 2014 | Stops add value only when returns trend → stops on the trend leg, time exits on the fade leg |
| Schmeling, Schrimpf & Todorov, BIS WP 1087 | Crowded funding/basis predicts crashes (kept as a later idea) |
| López de Prado 2018; Bailey et al. (DSR, PBO) | Triple-barrier meta-labels, purged walk-forward, overfitting checks |
| Cakici et al., *IRFA* 2024 | Simple models match complex ones in the crypto cross-section |
| freqtrade, Jesse, Hummingbot | Risk-on-stake stops for futures, triple-barrier exits, Monte Carlo trade resampling |

## What was tested

All figures at 125 USDT margin × 8x, K = 5 positions, both years.

| Family | Result | Verdict |
|---|---|---|
| Cross-sectional momentum / reversal (24 h–7 d residual vs BTC, long/short, 24–72 h holds) | Near zero or negative in Y1, mixed in Y2; profit factor 0.8–1.1 | Rejected as a standalone signal |
| Donchian breakout, no regime gate | Y1 PF 0.85–1.08 | Rejected |
| **Donchian breakout with regime gate** (BTC above its 20-day EMA and > 50 % of alts above their 7-day EMA for longs) | Positive in both years at every grid point (n = 20–80 4H bars, stop 2–4 ATR, volume 1.2–2×) | **Kept** (trend leg) |
| Retest (limit) entries after breakouts | Misses the runners; Y2 turns negative | Rejected |
| Profit locks (ATR trail after 2–8 R, break-even moves) | Cut the few large winners; Y2 negative | Rejected |
| Stall exits (close if < x R after y h) | No consistent gain | Rejected |
| ML meta-label on 45 k breakout events (ridge / LightGBM, monthly walk-forward, 20 features) | Monthly IC 0.07, but predicted-best decile has no higher mean R: the runners are not predictable | Rejected (complexity without economic gain) |
| Universe size 30 / 50 / 100 / 200 | Only the top 200 works: the runners are mid-caps that become hot | Universe = top 200 |
| **Weak-alt shorts in broad downtrends** (alt breadth < 40 %, bottom 5 % by 72 h residual return vs BTC, 72 h hold) | Y1 +1.8 k (PF 1.08), Y2 +7.0 k (PF 1.27, 83 % positive months), positive without the best five trades | **Kept** (bear leg) |
| Fixed notional vs fixed risk per trade | Same or better return/drawdown with fixed notional | Owner's fixed notional kept |

## The strategy

Two legs share the position slots (default 5), decided at each 4H close:

1. **Trend breakout.** In a bull state, long the alts that close above their N-bar 4H high on
   24 h turnover ≥ x × the 7-day daily average. Stop = 3 × ATR(14, 4H), clamped to [1 %, loss cap ÷
   leverage]. Exit when a 4H close falls back through the channel midline (no profit target: the
   profit comes from a few multi-fold runs). The aggressive style also shorts breakdowns when BTC
   is below its 20-day EMA and breadth < 50 %.
2. **Weak-alt shorts.** When breadth < 40 %, short the weakest 5 % by 72 h residual return against
   BTC (β over 7 days), ATR stop as above, close after 72 h.

Styles: **conservative** (n = 55, volume 1.5×, no breakdown shorts) and **aggressive** (n = 20,
volume 1.2×, breakdown shorts on).

Sizing: notional = margin × leverage; the order layer sizes `equity × riskPct × strength ÷ stop
distance`, so the strategy sends `strength = notional × d ÷ (equity × riskPct)` (the account's riskPct
must be ≥ 2 × margin ÷ equity). The order layer is isolated-margin and lowers the leverage so the
liquidation price stays beyond the stop; the notional and the loss at the stop are unchanged.

**Suggested margin.** With the margin fixed at one unit, a block bootstrap of daily P&L (5 000
one-year paths, 10-day blocks, daily P&L winsorised at 0.5 %/99.5 % so a giveback of an outsized
unrealised win is not resampled without the win) gives a 95th-percentile one-year drawdown of about
**60 margins (conservative) / 50 margins (aggressive)** at K = 5. The dial maps aggressiveness 0 → 1
to a drawdown budget of 10 % → 50 % of equity: `margin ≤ equity × budget ÷ (dd95 × K / 5)`, and also
`K × loss cap × margin ≤ 50 % of equity` (every position stopped at once).

| Equity | aggressiveness 0 | 0.5 | 1 |
|---|---|---|---|
| 3 000 USDT | 5 / 6 | 15 / 18 | 25 / 30 |
| 15 000 USDT | 25 / 30 | 75 / 90 | 125 / 150 |

(margin per trade in USDT, conservative / aggressive). The owner's habitual 125 USDT is "reasonable"
at about 15 000 USDT of equity on the most aggressive setting; at 3 000 USDT the bootstrap says a bad
year can take more than the whole account.

## Results of the strategy file (offline backtest, `research/altbt`)

Two years (2024-11-01 → 2026-10-10), the strategy file run through the SDK by `research/altbt`
(account limits: 5 positions, max leverage 8, riskPct 10 %, maxInstPct 30 %, no daily breaker).
"Y2 w/o Apr-26" removes the month of the RAVE run. Drawdown % is from the equity peak.

| Style | Deposit | Margin per trade | Trades | Net | Y1 | Y2 | Y2 w/o Apr-26 | Net w/o best 5 | Max DD | Lowest equity |
|---|---|---|---|---|---|---|---|---|---|---|
| conservative | 3 000 | 125 fixed | 1 211 | +65 986 | +9 075 | +56 911 | +5 089 | +6 325 | −34 794 (37 %) | 2 513 |
| aggressive | 3 000 | 125 fixed | 1 633 | +45 959 | +4 150 | +41 809 | +6 135 | +5 994 | −17 068 (50 %) | 2 392 |
| conservative | 3 000 | auto, aggressiveness 1 (starts at 25) | 1 211 | +25 441 | +1 988 | +23 453 | +3 292 | −1 977 | −16 907 (39 %) | 2 898 |
| aggressive | 3 000 | auto, aggressiveness 1 (starts at 30) | 1 633 | +20 093 | +887 | +19 206 | +4 045 | −1 820 | −7 890 (34 %) | 2 848 |
| aggressive | 3 000 | auto, aggressiveness 0.5 (starts at 18) | 1 633 | +9 739 | +566 | +9 173 | +1 726 | +105 | −3 703 (29 %) | 2 910 |

The offline run matches the research engine (conservative 1 211 vs 1 210 trades, aggressive 1 633 vs
1 593); it holds the RAVE run longer because the midline exit is checked on 4H closes, not hourly.
Auto margins compound with equity, so their drawdowns are mostly givebacks of earlier gains.

**Start-date risk** (125 USDT fixed margin; for each of 344 start days, trades opened in the following
365 days, realised P&L): the deepest dip below the starting equity was −2 848 (conservative) /
−2 912 (aggressive), 5th percentile −2 399 / −2 164; every one-year window ended positive (5th
percentile +6 282 / +4 149). The windows overlap heavily — two years of data are one or two
independent samples — and open-position losses are not included. With 3 000 USDT and 125 USDT
margins the worst historical start leaves ~100–150 USDT: the bootstrap's warning (dd95 ≈ 50–60
margins ≈ 6–7.5 k USDT) is the safer planning number.

## Limits and what would change the conclusion

- **Profit concentration.** Most of Y2 comes from one coin (RAVE, 0.45 → 16.8 USDT in April 2026).
  Without the best five trades both styles stay positive over two years, and Y2 without April 2026 is
  still positive, but the expected yearly profit is dominated by whether such runs happen and are
  caught. The trend leg loses slowly in choppy months.
- **Regime dependence.** Y1 and Y2 differ (Y2 is an alt-bear year with a few violent rallies). The
  bear leg only trades in broad downtrends; a long sideways market with breadth around 40–55 % gives
  neither leg much to do.
- **Survivorship and funding** (above) flatter longs and understate funding before 2026-07.
- **Liquidity.** 1 000 USDT notional is small for the top 200, but slippage on the smallest names
  during the moves that matter is likely worse than 5 bp.
- **In-app backtests** can only replay the ~50 days of 1H history kept in production.

## Reproducing and continuing

This is a long-running line of work; everything needed to pick it up is in the repository.

| Asset | Where |
|---|---|
| Literature notes (verified sources) | [research-alt-perps-literature.md](research-alt-perps-literature.md) |
| Research engine and every experiment above, with an index of questions and findings | [`research/altstudy/`](../research/altstudy/README.md) |
| Offline backtest of a strategy file (SDK runtime + Go backtest model) | [`research/altbt/`](../research/altbt/README.md) |
| Data: hourly OKX panel (2024-10-01 →, all USDT swaps, funding where served) | `data/altbt/panel.npz` from `research/altbt/fetch.py`; the owner's copy is on the server in `/opt/ctg-research/repo/data/altbt/`, extended weekly with `fetch.py --update` (instruments delisted after a download keep their history) |
| Strategy and tests | `strategies/library/alt_regime.py`, `strategies/tests/test_alt_regime.py` |

On the server, research runs in resource-capped throwaway containers so production is unaffected:
`/opt/ctg-research/bt.sh <script> …` (strategy image, numpy only, no network) for `research/altbt`, and the
`ctg-research` image (`research/altstudy/Dockerfile`) for `research/altstudy`. Workflow for a change:
idea → experiment in `altstudy` (both years, plateau, net without the best five) → port into the
strategy → `altbt` run of the strategy file → update this note and the strategy's `dd95` when the
risk profile changes.

## Next ideas (not implemented)

- A funding/basis crowding filter for the trend leg (BIS WP 1087).
- Monitoring the live paper account against the backtest's monthly distribution before any real use.
