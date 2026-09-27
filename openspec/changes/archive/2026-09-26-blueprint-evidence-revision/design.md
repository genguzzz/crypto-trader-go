## Evidence and provenance

All numbers come from OKX v5 public REST/WS on 2026-09-26 (UTC 16–18h, Saturday) and 1H history 2026-08-06→2026-09-26; outputs committed in `research/poc/results/`. Units: turnover = sum of candle `volCcyQuote` (USDT for USDT-settled linear swaps; cross-checked against `volCcy×close` within 1.8% per bar); OI = `oiCcy` (base coin); funding normalized per hour using `nextFundingTime−fundingTime` (4h or 8h observed).

## Decisions

1. Universe filter adds `ctType=linear`; all instrument categories (crypto, stock/ETF, commodity) are included by user decision, exposed as a column/filter; threshold stays >10M USDT.
6. The I0 recorder runs 24/7 on the home NAS/N1 (arm64 build, OKX reachability and disk budget to be verified in I0).
2. Active pool recomputed once per confirmed minute after all bars arrive or minute+5s timeout; stragglers marked stale. 5s cadence publishes prices/freshness only.
3. Tickers WS limited to active ∪ watchlist ∪ held; full-pool REST ticker every 30–60s.
4. Scores: rule engine over a fact panel; every rule's hourly snapshots are immutable and evaluated out of sample (rank IC, net quintile spread) — the only allowed evidence of usefulness.
5. Slice order I0 recorder → P prototype (recorded public data) → I1 → I2 → I3 → I4.

## Failure handling

Unchanged from the archived blueprint (gap fill before LIVE, unknown ≠ zero, account unknown ≠ flat). Newly measured: OKX emits zero-volume bars, so a missing minute always indicates a transport gap.

## Limitations

Single weekend snapshot for WS/turnover; 51-day single-regime factor study with survivorship bias and 9 simultaneous hypotheses. Conclusions are "preset direction unsupported", not "reversal is profitable".
