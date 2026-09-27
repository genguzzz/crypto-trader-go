## Provenance, units, freshness

OKX v5 public only. Turnover = Σ `volCcyQuote` (USDT) of confirmed 1m bars; OI = `oiCcy` (coin) sampled per confirmed minute from WS (≤2 min old) plus rubik 5m/1H history at bootstrap/replay; funding = last settled rate ÷ interval hours (4h/8h), current-period rate shown separately (live only); basis uses OKX same-name USDT spot (15s REST); spread from tickers (≤2 min old). Exchange `ts` retained; a missing minute is a transport gap (OKX emits zero-volume bars).

## Failure handling

- Minute publish waits until every instrument that had bar m−1 has bar m, or m+65s; stragglers are `stale` and gap-filled (oldest missing minute first, paging back as far as needed).
- Business WS reconnect → REST gap fill for all instruments; SSE clients refetch the snapshot on every `universe` event, so dropped events self-heal.
- Features with any missing input are absent (never 0); rules with a missing weighted input are unrated.
- Replay uses hourly bars/rubik 1H OI with conservative alignment (snapshot at hour start used only after that hour closes); rows flagged as replay with an assumed 2 bps spread; live records are separate and immutable per rule version.

## Measured (2026-09-26, Apple Silicon)

Cold bootstrap 4m14s (4,054 REST requests, 0 subscription errors); restart 1m06s; RSS ≈165 MB with GOMEMLIMIT=160MiB; pool 47 identical to the independent probe; 0 data races under `-race` during a live run.
