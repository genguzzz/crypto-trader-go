# Research probes (2026-09-26)

One-off programs that tested the key assumptions behind [product decisions](../../docs/decisions.md). Public OKX endpoints only: no credentials, no private endpoints, no orders. A separate Go module, not production code.

| Program | What it checks | Run (in this directory) | Time |
|---|---|---|---|
| `cmd/turnover` | exact 12h USDT turnover over 720×1m for the whole universe, warm-up requests/duration, how many clear the threshold, whether 5m/1H/24h-ticker approximations pick the same pool, minute gaps | `go run ./cmd/turnover -rate 20 -out results/turnover-<ts>.json` | ~4 min (50% of quota) |
| `cmd/wsprobe` | one connection subscribing the whole universe to `candle1m` (business) / `tickers` / `open-interest` / `funding-rate` (public): acks, per-minute confirm arrival and latency, message rates | `go run ./cmd/wsprobe -d 4m30s -out results/ws-<ts>.json` | as given |
| `cmd/factorstudy` | ~51 days of 1H data: point-in-time pool size by weekday, cross-sectional Rank IC of candidate factors vs forward H-hour returns, quantile long-short spreads after costs | `go run ./cmd/factorstudy -h 4 -out results/factorstudy-h4.json`; reuse the cache with `-end 20260926T15` | ~35 min first run (rubik OI limit 5/2 s), seconds from cache |
| `cmd/sqlitebench` | pure-Go SQLite WAL: a 473-row transaction per minute, disk growth, 12h aggregate and single-coin chart queries | `go run ./cmd/sqlitebench -days 7` | ~2 min |

`results/*.json` are the 2026-09-26 outputs (Saturday, 16–18 UTC), committed as evidence; `results/cache/` (raw responses, ~60 MB) is ignored. Markets change, so reruns give different numbers — check whether the conclusions still hold.

Test machine: Apple Silicon macOS, Go 1.24.4, home broadband (OKX routed to `TKY`).
