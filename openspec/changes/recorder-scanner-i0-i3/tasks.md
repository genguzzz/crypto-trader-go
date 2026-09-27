## 1. Implementation (this change)

- [x] 1.1 OKX public REST/WS adapter with rate budgets, reconnect, batched subscribe diff, optional proxy/DoH
- [x] 1.2 SQLite store (1m/1H candles, OI, funding, universe, rules, score snapshots, watchlist, retention)
- [x] 1.3 Recorder: bootstrap (5m fast pool → 1m/1H backfill → OI/funding history), live WS, gap fill, restart recovery
- [x] 1.4 Per-minute universe (all categories, >10M, cap 200), removals with reasons, provisional flag
- [x] 1.5 Fact panel (shared live/replay `features.Compute`), rule engine + presets, hourly snapshots, replay, track statistics
- [x] 1.6 HTTP + SSE API, embedded Vue UI (scanner desktop split / mobile routes, detail charts, rules & record, status, positions placeholder)
- [x] 1.7 Unit tests (features, rules, track, window/aggregate/funding, deep gap refill against fake OKX); race run
- [x] 1.7b 10s intraday preview, 30d hourly / 14d replay / 7d 1m history, coin icons, runtime settings page (default threshold 5M USDT)
- [x] 1.8 Server deployment — old-server Docker (distroless + bind-mounted binary, deploy.sh swap/rollback), https://ctg.gugenzzz.top, local DB reused (NAS dropped: its OpenClash cannot route okx.com)
- [ ] 1.9 I0 exit criterion: 72h continuous run with ≥99.9% minute coverage and 0 unrepaired gaps
- [ ] 1.10 Archive after 1.8–1.9

## 2. Future

- [ ] I4 read-only positions / external events (separate change, needs user approval)
