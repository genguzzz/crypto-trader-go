# Data sources, freshness and retention

OKX v5 **public** endpoints (no credentials, no orders), plus the CoinGecko free API (market cap, display only). The one exception is the owner's own positions: when configured, signed read-only GETs of positions, pending TP/SL and equity (fetched on demand, cached ~3 s; never orders). Timestamps follow exchange time; missing minutes/data = unknown, never 0. To check freshness: MCP `get_system_status`, or [系统状态](/status).

## Recorded locally vs fetched on demand

| Data | Method | Range / frequency |
|---|---|---|
| 1m candles | **Recorded**: WS business channel subscribed for all pool-eligible contracts, REST fills gaps | Real time; confirmed every minute |
| Open interest (OI, coin-denominated) | **Recorded**: WS public, whole pool, sampled ~every minute; older data from OKX rubik history (1H ≈ 60 days, 5m ≈ 5 days) | Fine-grained OI history can't be downloaded, only recorded |
| Funding | **Recorded**: current/predicted rate + every settlement; settlement history backfilled ~3 months (OKX's limit) | Settles every 4h or 8h |
| Ticker | WS only for active pool and watchlist; whole-market REST fallback every 30 s | — |
| Trades → order flow/footprint | **Recorded**: active pool ∪ watchlist ∪ contracts viewed in the last 30 minutes, aggregated per minute | Opening the instrument page backfills missing minutes within retention (OKX daily trade files + historical trades endpoint) |
| Taker buy/sell volume (no footprint) | Fetched on demand from OKX rubik: 5m ≈ 5 days, 1H ≈ 30 days | For older candles without recorded trades |
| Liquidation orders | Same contract set as order flow, synced every minute (OKX keeps only ~24h); stored locally 30 days | Synced every 10 s while the instrument page is open |
| Liquidation map | **Estimated** from candles + OI, not exchange data; cached 5 s per contract | See instrument-page.md |
| 1H candles | Recorded + daily backfill to 400 days or listing date (including delisted contracts, for backtests) | — |
| Spot price / spot candles | Spot ticker every 15 s; spot candles (the chart's "Spot" (现货) overlay) fetched from OKX on demand | ~88% of the active crypto pool has a same-name USDT spot pair |
| Order book depth | Live snapshot, refreshed every second, no history kept | Up to 5000 levels per side |
| Market cap / circulating supply | CoinGecko top 1500, ~every 2 hours | Display only |
| Score snapshots | One per rule every hour on the hour, immutable, **never deleted** (for track record) | — |
| Pool snapshots | Every minute, kept 30 days | — |

**Contracts outside the list**: all OKX USDT perpetuals (including stocks, commodities, pre-market) have only a catalog and 30 s quotes in memory. Opening one downloads on demand (last 24h and last 7 days of 1m, 30 days of 1H, OI, funding), then records live for 30 minutes after viewing; adding to the watchlist subscribes long-term. Meanwhile the instrument page shows "Syncing" (同步中).

## Retention and tiers

Each contract gets its highest tier: **Watchlist** (自选) > **Active pool** (活跃池: in the pool or left less than 7 days ago) > **Recently viewed** (最近查看: instrument page opened within 3 days) > **Pool-wide recording** (全池录制: in a selected category, not in the pool) > **Untracked** (未跟踪: delisted/outside categories and not viewed).

| Data | Watchlist | Active pool | Recently viewed | Pool-wide | Untracked |
|---|---|---|---|---|---|
| 1m candles / OI samples | 30 days | 7 days | 7 days | 2 days | deleted |
| 1H candles | 400 days | 400 days | 400 days | 400 days | 400 days |
| Funding settlements / intraday observations | 400 / 7 days | 400 / 3 days | 400 / 3 days | 400 / 3 days | 400 / 1 day |
| Order flow (trades + footprint) | 14 days | 3 days | 3 days | not recorded | not recorded |
| Taker buy/sell volume (rubik) | 90 days | 30 days | 30 days | 7 days | 3 days |

- Storage cap defaults to 2 GB ([设置](/settings) → Data storage (数据存储)), with per-type quotas. Every 30 minutes data is deleted per the table above; if a type exceeds its quota, retention is cut ×2/3 starting from the lowest tier until back at 90% of quota, but never below a floor (e.g. 1m/OI 26 hours, watchlist 3 days). Tightened cells are flagged on the System status page; its "actual retention" (实际保留期) is authoritative.
- Freed space is reused inside the database; files don't shrink. Pool-wide 1m is ~60 MB/day.
- Score snapshots, rules and settings are never deleted.

## Startup phases (top-bar label)

`启动中` (starting) → `快速建池` (quick pool build: 5m approximation, "provisional list" 临时名单, ~30 s) → `回填中` (backfilling the last 12h of 1m for all contracts, ~4 minutes) → `实时` (live). Then in the background: 14-day rule history replay, 7-day 1m backfill for active pool and watchlist, 1H history backfill (progress on System status). After a restart it catches up in ~1 minute.

## Clocks

| Frequency | What |
|---|---|
| WS push (~0.1–7 s) | Open candle, last price, OI, funding |
| Every 2 s | Last-price push to pages |
| Every 10 s | Intraday preview: facts panel and scores recomputed with the open minute (changes, not in track record) |
| Every closed minute | 12h window → pool add/remove → facts panel → rule scores → alert checks |
| Hourly | Save immutable score snapshots |

## Time zones

- UI times default to **UTC+8**; [设置](/settings) → Display (显示) can switch to UTC or local time (this browser only; data unchanged).
- Candles align to UTC (4H at 0/4/8/12/16/20 in UTC+8; 1D = UTC 00:00).
- Daily-loss circuit breaker, daily returns in reports and OKX daily trade files all split days in UTC+8.
- API / MCP times are millisecond timestamps.

## System status page [系统状态](/status)

- **Health checks** (refresh every 3 s): phase not live, or latest confirmed minute stale > 3 minutes → warning/error; WS disconnected → error, connected but no message for 30 s → warning; any contract data gap → warning, > 5% → error; REST error rate > 1%, storage ≥ 90% or retention tightened → warning. Clicking an issue tag jumps to its section.
- **Data freshness**: uptime, latest confirmed minute, background job progress (1m backfill, history replay, 1m history backfill, 1H history backfill).
- **Active pool coverage**: count of contracts with complete 12h window / warming up / data gap / listed < 12h.
- **OKX connection**: business / public WS subscriptions, messages, reconnects; REST requests, errors, rate limits.
- **Data storage**: tiered retention table (with current actual retention), usage per type. **Process**: memory, goroutines and other debugging info.
