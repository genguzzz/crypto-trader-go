# Architecture and layout

## 1. Overview: separate backend and frontend, one database

```
            OKX v5 public API (no key)
   REST (token-bucket limited)         WebSocket
   instruments/tickers/candles/        business: candle1m for the whole universe
   OI history/funding                  public: OI and funding for all; tickers only for active pool + watchlist
              │                        │
              ▼                        ▼
   ┌──────────── Go backend (cmd/server) ───────────────────────────┐
   │ internal/okx      adapter: rate limits, retries, reconnects, subscription diffs, proxy/DoH │
   │ internal/market   engine: record → fill gaps → per-minute pool → fact panel            │
   │                   → rule scoring → 10 s intraminute preview → hourly snapshot          │
   │                   → 14-day replay → 7-day 1m backfill                                  │
   │ internal/features fact-panel formulas (shared by live and replay)                      │
   │ internal/rules    rule definitions, validation, scoring, preset reference rules        │
   │ internal/track    track record (Rank IC, t, quantile returns, costs)                   │
   │ internal/alerts   monitoring rules: checked every minute → Telegram/webhook delivery   │
   │ internal/store    SQLite (WAL) + database/sql pool                                     │
   │ internal/api      REST JSON + SSE push + icon proxy                                    │
   └──────────────┬───────────────────────────┬─────────────────────┘
                  │ /api/v1/* (JSON / SSE)    │
                  ▼                           ▼
   Frontend web/ (Vue 3 + TS + Vite + Lightweight Charts)   SQLite file data/ctg.db
   Dev: Vite dev server proxies /api; prod: UI embedded in the binary, run in Docker (§7)
```

- **Separation**: the backend exposes only `/api/v1/*`; the frontend is its own Vite project. For convenience the build output is embedded with `go:embed` (`-web=true`, default); with `-web=false` the backend serves only the API and the UI can come from the Vite dev server or (later) its own nginx container.
- **Database and pool**: one SQLite WAL file, `database/sql` pool (`SetMaxOpenConns(4)`, `busy_timeout` 10 s); writes are batched by a single writer goroutine every 2 s so reads and writes don't block each other. Hot data (the last 1500 minutes of 1m candles) lives in in-memory ring buffers. If multiple instances or much larger data are ever needed, `internal/store` is the only place to swap for PostgreSQL.

## 2. Data flow and clocks

| Clock | What happens |
|---|---|
| WS push (~0.1–7 s) | update the open candle, last price, OI, funding |
| Every confirmed minute (all bars in, or +65 s at the latest) | 12h window → pool adds/removes (with reasons) → fact panel → rule scores → SSE `universe` |
| Every 10 s | recompute fact panel and scores for the active pool / watchlist using the open minute (`preview`, not final) |
| Every 2 s | SSE `prices` pushes last prices |
| Every confirmed minute (after the above) | monitoring rules checked (`internal/alerts`, edge-triggered with cooldown), Telegram/webhook delivered asynchronously |
| Every hour | persist each rule's scores for the active pool (immutable, used by the track record) |
| 15 s / 30 s / 5 min | spot tickers, swap ticker fallback (also the search catalog's quotes), instrument catalog refresh |
| 30 min | retention: delete by tiered retention; when over quota, tighten from the lowest tier (`retention.go`) |
| Start / settings change | quick 5m pool → 1m/1H backfill → 14-day hourly replay → 7-day 1m history backfill |

## 3. Key algorithms (summary)

- **Monitoring pool**: sum of `volCcyQuote` over 720 consecutive confirmed 1m candles > threshold (default 5M USDT), top N by turnover (default 200). A missing minute ≠ zero volume (OKX sends zero-volume candles for idle minutes); gaps mark the instrument `stale` and are refilled from the earliest missing minute.
- **Fact panel**: 1h/4h/24h returns, 4h return vs BTC, 4h trend strength (normalized by 48h hourly volatility), coin-denominated OI change, volume surprise (vs 7-day median), funding per hour (4h/8h intervals normalized), basis, spread. Missing means unknown.
- **Rule scoring**: each field's percentile within the active pool → mapped to ±1 → weighted average → `50 + 50 × Σwx / Σ|w|`. Long and short are independent; any missing input means unscored; the spread gate is separate.
- **Search catalog**: every live OKX USDT-margined linear perpetual (including stocks/commodities and pre-market), kept in memory only: instrument metadata + a 30 s ticker (~480 rows). Contracts outside the list download history on demand when opened (last 24h 1m, 30 days 1H, OI, funding, last 7 days 1m) and are streamed live for 30 minutes after viewing; watchlisted ones stay subscribed.
- **Order flow**: the public WS `trades` channel (one message per taker order per price, side = aggressor) is aggregated per minute for active pool ∪ watchlist ∪ contracts viewed in the last 30 minutes: taker buy/sell USDT, trade count, and a per-price footprint (~1 bp, never finer than tick). Only minutes fully covered by a live subscription count (`orderflow_1m`, levels packed as int32 + 2×float32). Older or unrecorded ≥5m candles take delta from OKX rubik taker volume (5m ≈ 5 days, 1H ≈ 30 days, `taker_volume`) without a footprint. `GET /orderflow` merges by candle; all footprints in one response share a step (base step ×1/2/10/20/100…, each divides the next) chosen so the 90th-percentile candle has ≤ 40 levels, and unusually wide candles are merged again on their own. Opening a detail page (`syncInst`, at most once per coin per 30 min) backfills missing minutes within the order-flow retention (`flowbackfill.go`): full days from OKX's daily trade files (`/public/market-data-history`, UTC+8 days, published the next day, aggregated line by line), the current unpublished day from `/market/history-trades` (backwards from the start of live recording, at most 600 pages per run); only fully covered minutes are written, live-recorded minutes are never overwritten, and only one backfill runs globally at a time. The frontend (`charts/footprint.ts`) merges levels further by vertical zoom (≥ 12 px per row) and computes POC, the 70% value area, diagonal imbalances (3× with a median floor), stacked imbalances (≥ 3 levels) and naked POCs.
- **Liquidations**: OKX public `liquidation-orders` REST (every fill, unthrottled, OKX keeps ~24h; the WS channel pushes only one per contract per second) is synced each minute for the order-flow set and stored for 30 days (`liquidations.go`). `GET /liquidations` serves per-bar long/short liquidation totals and recent fills; `GET /liqmap?days=1|7` serves an *estimated* liquidation map (the same kind of model as CoinGlass's liquidation map): OI increments spread across 5–100x leverage tiers at their approximate liquidation prices, shrunk as OI falls and removed once price trades through. Steps are 5m (5m candles + per-minute OI, including the forming bar and latest OI sample) where that data is unbroken (`fine`), hourly before; each bin carries per-tier amounts (`longBy`/`shortBy`, order of `levers`) so the chart stacks bars by leverage. Cached 5 s per instrument; an open chart refetches it and the liquidation bars every 10 s (a liquidation query syncs OKX at most every 8 s). Amounts are OKX-only USDT estimates; CoinGlass aggregates exchanges with its own leverage mix and relative scale, so compare cluster positions, not amounts.
- **Market cap / unlocks**: CoinGecko free API, top 1500 (refreshed ~2 h, symbol match takes the largest market cap); display only, never used by the pool or scores.
- **Open interest bar**: scanner rows carry `oiUsd` (latest OI in USD, ≤ 15 min old). The UI fills each row by `r/(1+r)`, `r = √(OI / half point)`; the half point (default 5M USDT) is a per-browser display setting.
- **Data tiers and retention**: each instrument takes its highest tier: watchlist > active pool (in the pool or left < 7 days ago) > recently viewed (3 days) > universe recording > untracked. Each table has its own retention and quota (default cap 2 GB: 1m 45%, OI 10%, 1H 10%, funding 5%, order flow 18%, taker volume 2%, other 10%):

  | Table | Watchlist | Active pool | Recently viewed | Universe | Untracked | Floor under quota pressure |
  |---|---|---|---|---|---|---|
  | 1m candles / OI samples | 30 d | 7 d | 7 d | 2 d | deleted | 26 h (watchlist 3 d) |
  | 1H candles | 180 d | 60 d | 60 d | 30 d | 30 d | 21 d (watchlist 30 d, untracked 7 d) |
  | Funding settled / intraperiod observations | 365 / 7 d | 90 / 3 d | 90 / 3 d | 90 / 3 d | 30 / 1 d | 14 d / 1 d |
  | Order flow (1m + footprint) | 14 d | 3 d | 3 d | deleted | deleted | 12 h (watchlist 2 d) |
  | Taker volume (rubik) | 90 d | 30 d | 30 d | 7 d | 3 d | 1–7 d |

  Over quota, retention is multiplied by 2/3 tier by tier starting from "untracked" until usage is back under 90% of the quota or the floor is reached; on-demand and backfill downloads follow the effective retention so nothing is downloaded only to be deleted. Score snapshots, rules and settings are never deleted; pool snapshots are kept 30 days.
- **Track record**: Spearman Rank IC between score rank and forward h-hour return rank within the pool at the same time, sampled without overlap; t-stat; top-quintile excess and net return after 5 bps taker per side plus spread.

See [product decisions](decisions.md).

## 4. Technology choices

| Choice | Why |
|---|---|
| Go 1.25 | long-lived connections and concurrency, static single binary (no CGO), trivial cross-compilation |
| Own narrow OKX adapter (`net/http` + gorilla/websocket) | only ~8 REST endpoints + 5 WS channels needed; precise control of units and timestamps; one connection subscribes the whole universe without errors in tests |
| SQLite (modernc, pure Go) + pool | measured ~12 ms per full-universe minute write, ~4 ms for a 3-day candle query; no separate database service |
| HTTP + SSE | one-way push is enough; the client refetches the snapshot on each event, so it self-heals |
| Vue 3 + TypeScript + Vite | type safety, fast builds; Lightweight Charts for candles (Apache-2.0, attribution kept) |

## 5. Directory layout

```
cmd/server/            entry: flags, networking (proxy/DoH), start engine and HTTP
internal/okx/          OKX REST/WS adapter, rate limits, dialing
internal/store/        SQLite schema and access
internal/market/       engine: engine.go (startup/recording/WS), universe.go (per-minute pool), catalog.go (search catalog / on-demand download),
                       retention.go (tiered retention),
                       preview.go (10 s intraminute), rules.go (rules/replay/track record),
                       history.go (7-day 1m backfill), settings.go (runtime settings),
                       queries.go (chart/detail/status queries), state.go (in-memory structures and series)
internal/features/     fact panel
internal/rules/        rules and scoring
internal/track/        track-record statistics
internal/alerts/       monitoring rules (types registered in conditions.go), delivery channels (masking/sending/retries), history
internal/trade/        paper-trading order layer (docs/trading-design.md): accounts, instances, signals, risk/sizing, SimBroker, backtest replayer, /api/v1/trade/*; trade/mdlive adapts market.Engine (market/tradeapi.go)
internal/devapi/       external access: API keys / tokens, rate limits, endpoint registry → open REST API, OpenAPI document, MCP server
internal/api/          HTTP routes, SSE, icon proxy, gzip
web/                   frontend (src/views pages, src/components components, embed.go embeds the build)
deploy/                docker-compose.yml (server runtime), deploy.sh (build/upload/swap/rollback)
research/poc/          research probes (separate Go module, not production code)
docs/                  documentation
```

## 6. API (/api/v1)

`GET health|status|meta|settings|catalog|storage|orderflow|liquidations|liqmap|universe?scope=active|all|watch|instruments/{id}|candles|compare|series|rules|rules/{id}/track|icons/{ccy}.png|stream(SSE)`;
`PUT settings|watchlist/{id}`, `POST rules`, `DELETE rules/{id}|watchlist/{id}`; `GET positions` always returns "not connected" (until I4).
Monitoring: `GET|POST alerts/rules`, `PUT|DELETE alerts/rules/{id}`, `GET|PUT alerts/channels` (secrets are returned masked; empty/masked = unchanged), `DELETE alerts/channels/{telegram|webhook}`, `POST alerts/channels/{ch}/test`, `GET alerts/history?limit=`.

Developer (owner UI, same access as the rest of `/api/v1`): `GET|PUT dev/config` (external API switch, server-wide limit), `GET|POST dev/keys`, `PATCH|DELETE dev/keys/{id}` (rename, limits, revoke), `GET dev/log` (last 200 external calls, memory only), `GET dev/endpoints` (registry for the Dev tab).

### External access for agents (`internal/devapi`)

- **Credentials**: API keys (`ctg_sk_…`, optional expiry) and tokens (`ctg_at_…`, 1 h–30 d). Only the SHA-256 hash is stored (`api_keys` table); the secret is returned once on creation. Sent as `Authorization: Bearer <key>` or `X-API-Key`.
- **Rate limits**: one token bucket per key (capacity = burst, refilled at the per-minute rate) plus a server-wide bucket (`GlobalPerMin`, burst 1/6 of it) that protects the recorder. Each endpoint has a cost (1 by default; order book 3, footprint levels 3, instrument / liquidation map / spot candles / `scope=all` 2, usage 0). Responses carry `X-RateLimit-Limit|Burst|Remaining|Reset|Cost`; 429 adds `Retry-After` and `retryAfterMs`. Usage counters are flushed to SQLite every 30 s.
- **One registry, three surfaces** (`endpoints.go`): each read-only query is served as `GET /api/open/v1/<path>`, documented in `GET /api/open/v1/openapi.json` (no key needed) and exposed as an MCP tool at `POST /mcp` (streamable HTTP, stateless, JSON responses, protocol 2025-06-18 / 2025-03-26 / 2024-11-05; rate-limit errors come back as `isError` tool results). CORS is open because authentication uses headers only.
- **Exposed**: data freshness (phase, last minute, coverage, stream connectivity, job progress), field definitions, pool snapshot (filter/sort/limit, one rule's scores by default), catalog search, instrument detail, candles (swap/spot), comparison with BTC/ETH, OI / funding series, order book (depth-limited), order flow, liquidations, estimated liquidation map, rules, track records, fired alert events (without delivery details). **Not exposed**: settings, alert channels, storage/memory/database internals, anything that writes.
- The Settings page switch disables all external calls (503) without deleting keys; the Dev tab (shown by default, per-browser toggle in Settings) manages keys and has a request tester.

## 7. Deployment

```
workstation deploy/deploy.sh ──scp static binary──▶ ctg-prod:/opt/ctg/bin/ctg ──(bind mount, ro)──▶ container ctg
                                                                               distroless/static:nonroot (2 MB)
public 443 ─▶ nginx stream (SNI default route) ─▶ 127.0.0.1:8444 gateway-ctg.conf (TLS *.gugenzzz.top) ─▶ 127.0.0.1:8686
```

The image contains no application code; an upgrade swaps the binary and restarts the container. Data lives in `/opt/ctg/data` (SQLite), decoupled from binary versions. See [operations](operations.md).
