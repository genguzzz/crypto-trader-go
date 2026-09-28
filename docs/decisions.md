# Product decisions

The principles and measured facts that the implementation relies on. Earlier planning documents (the 2026-09-19 research report, the end-to-end blueprint and the OpenSpec change records) are superseded by this file and remain in git history only.

## Principles

- **Research tool, not a trading bot.** Public OKX market data only; no orders, ever, without a separate explicitly approved project. Account access, if added, is read-only and needs the owner's approval.
- **Facts, rules, track record.** The fact panel is direction-free. A score is a user-defined rule's rank within the pool — not a probability or advice — and only the out-of-sample track record can show a rule is useful. Every rule is labeled "unvalidated" until it earns that.
- **Long and short are independent.** They are not complements of 100, and a weak long score is not a short signal.
- **Unknown is not zero.** Missing inputs leave a score unrated; missing data shows "—" or `unknown`, never 0 or "safe".
- **Live and replay share one formula** (`features.Compute`, `rules.Score`). Hourly snapshots are immutable; 10-second preview values never enter the track record.
- **Don't over-read the data.** Open interest is held by both sides and doesn't reveal who is buying; volume has no direction; an upcoming unlock doesn't imply a drop. News, unlocks and holder data are risk tags, not score inputs, until validated.
- **Record first.** Fine-grained OI and order-flow history can't be downloaded later (see below), so recording runs continuously and the server database is the single source of truth.

## Definitions

- **Eligible:** `state=live`, USDT-settled linear perpetual, not pre-market, category in the configured set (crypto / stock & ETF / commodity; all by default).
- **12h turnover:** sum of `volCcyQuote` over 720 consecutive confirmed 1m candles. A missing minute is a data gap (to be refilled), not zero volume; OKX sends zero-volume candles for idle minutes.
- **Monitoring pool:** eligible contracts with a complete window and 12h turnover strictly above the threshold (default 5M USDT), top N by turnover (default 200). No hysteresis; removed contracts stay flagged for 30 minutes.
- **Funding** is always normalized to an hourly rate (OKX mixes 4h and 8h intervals).
- **OI change** uses coin-denominated `oiCcy`, so price moves don't look like new positions.

## Measured facts (public API, 2026-09-26)

Reproducible with the probes in [`research/poc/`](../research/poc/README.md).

| Topic | Finding | Consequence |
|---|---|---|
| Pool size | 473 eligible contracts; above 10M USDT only 24–95 (median 52) over 51 days; #200 had ~0.5M | the threshold, not N, sets the pool size; default lowered to 5M |
| Categories | 182 stock/ETF and 8 commodity perps among the 473 | all categories enter the pool; category is a column and filter |
| Fast start | a 5m × 144 window picks the same pool as the exact 1m window | start with 5m, backfill 1m in the background |
| WS capacity | one business connection streams `candle1m` for all 473; confirm latency p50 0.7 s, p95 1.4 s | the per-minute universe window is feasible |
| Ticker bandwidth | full-universe tickers ≈ 1,300 msg/s, ~0.5 MB/s | WS tickers only for the active pool and watchlist; REST ticker every 30 s as fallback |
| OI history | OKX rubik OI: 5m ≈ 5 days, 1H ≈ 60 days | fine-grained OI must be recorded by us |
| Spot coverage | 218/473 have OKX USDT spot, but ~88% of the active crypto pool does | spot comparison is useful where it matters; otherwise `unknown` |
| Score direction | the intuitive "trend + OI confirmation" rule had negative Rank IC (1h −0.017, 4h −0.027); short-term reversal vs BTC was the strongest single factor | never hard-code factor direction; see [trend-signal research](research-trend-signals.md) |
| Storage | pure-Go SQLite WAL: ~12 ms per full-universe minute write, ~4 ms for a 3-day single-coin query | SQLite is enough; tiered retention keeps it under the cap |

## Phases

| Phase | Scope | Status |
|---|---|---|
| I0–I3 | recorder, per-minute pool, fact panel, rules and track record, web UI | live |
| I0 exit | 72h continuous run, ≥ 99.9% minute coverage, 0 unrepaired gaps | open (see roadmap) |
| I4 | read-only positions and exit assessment, news/unlock/on-chain sources | needs approval |
| Trading | automated orders | out of scope |
