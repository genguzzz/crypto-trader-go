# Monitored pool and Scanner

## Monitored pool (active pool) definition

- **Eligible**: OKX USDT-margined linear perpetuals in `live` state, not pre-market, in a category ticked in Settings (crypto / stocks·ETF / commodities — 加密 / 股票·ETF / 商品; all by default).
- **12h turnover** = sum of `volCcyQuote` (USDT turnover) over the latest **720 contiguous, confirmed (closed) 1m candles**, recomputed each confirmed minute.
  - Any missing minute = data gap, not zero volume (OKX sends zero-volume candles for idle minutes too); contracts with gaps are excluded from ranking for now (status stale), and the system backfills from the earliest missing minute.
  - `volCcy24h` (OKX's 24h volume) is not USDT turnover and isn't used for pool entry.
- **Entry**: 12h turnover **strictly above the threshold** (default 5 million USDT), top **N** by turnover (default 200). If fewer than N qualify, only that many are shown; low-liquidity contracts are never added to fill up. In practice the threshold sets pool size; N is rarely reached.
- No hysteresis: re-evaluated every minute. Removed contracts stay in the Scanner's "Recently removed" (最近移出) for 30 minutes with the reason.
- New contracts listed < 12h join only after 12h; contracts that just started recording are "warming up" (预热中).
- Threshold, N and categories are set in [设置](/settings) → Monitored pool (监控池); "Expected pool" (预计入池) estimates instantly from the latest 12h turnover; after saving, the server's recomputation is authoritative.
- At startup, before the 1m window is complete, 5m × 144 candles are used as an approximation ("provisional list" 临时名单; measured to match the exact result).

## Top-bar status (on every page)

- `qualified/cap` (达标数/上限): current pool count / N; the threshold next to it.
- First time: latest confirmed closed minute; pool changes happen only then.
- Second time (blinking): intraday preview time, recomputed every 10 s with the open minute; previews change and don't enter the track record.
- Labels: `临时名单` (provisional list), `快速建池` (quick pool build), `回填中` (backfilling), `启动中` (starting); dot green = push connected, yellow = starting/backfilling, red = disconnected, reconnecting.
- On PC the top bar also shows BTC / ETH price and 24h change (click to open).

## Scanner page [扫描](/)

List = active pool + watchlist (watchlist items not in the pool are marked "inactive" (非活跃)).

**Columns** (table on PC; cards on mobile):

| Column | Meaning |
|---|---|
| ☆/★ | Add/remove watchlist |
| Contract (合约) | Status dot (green live, red stale gap, yellow other), category tag (stocks/commodities), `非活跃` (inactive), `门槛` (gate: current rule's spread gate failed) |
| Long / Short (多 / 空) | Current rule's long and short scores (0–100, 50 neutral; "—" = unscored) |
| Fund/h (费/h) | Settled funding per hour (`funding_hourly`), colored by 8h-equivalent level: normal ≤ 0.01%/8h, elevated ≤ 0.05%, high ≤ 0.1%, extreme > 0.1% |
| Price (价格) | Live last price |
| 4h / vs BTC / OIΔ4h (4h / 相对BTC / OIΔ4h) | `ret_4h`, `excess_btc_4h`, `oi_chg_4h`, shown as percentages |
| Vol surprise (放量) | `vol_surprise_4h`, shown as multiple ×N (last-4h turnover ÷ 7-day 4h median) |
| Market cap (总市值) | CoinGecko market cap (display only) |
| 12h turnover (12h额) | 12h USDT turnover; `*` = provisional value |

Each row's background bar = open interest (USDT): fill = r/(1+r), r = √(OI ÷ half-full point); half-full point defaults to 5 million (Settings → Display (显示), this browser only).

**Controls**:
- Contract search; direction toggle All / Long / Short (全部 / 做多 / 做空) (decides which side "sort by direction score" uses; All = the higher of long and short).
- Scoring rule dropdown (temporary switch; default rule set in Settings); the ⓘ next to it explains scoring; the chart icon opens that rule's track record.
- Category filter (crypto/stocks/commodities), `自选` (watchlist), `门槛通过` (gate passed: only contracts passing the current rule's spread gate).
- Sort: by direction score, long score, short score, market cap, open interest, 12h turnover, or any facts-panel field (funding by absolute value); ascending/descending. Unknown values always sort last.
- `最近移出` (recently removed): contracts removed from the pool in the last 30 minutes, with reasons.
- When searching, "Other OKX contracts" (OKX 其他合约) lists OKX USDT perpetuals not in the list (all categories, including pre-market), marked `未录制` (not recorded: category not selected or pre-market) or `池外` (outside pool: recorded pool-wide but not in the pool). Opening one downloads history on demand and records live while viewing; adding to the watchlist keeps it long-term.

The list re-sorts every 10 s with the preview; re-sorting pauses while a finger/mouse is held down.

## Watchlist (自选)

- Add via the row star on the Scanner or the "自选" button on the instrument page. Watchlist contracts always show in the list, are always scored, get the highest data retention tier (see data-sources-and-freshness.md), and stay subscribed to live quotes and trades.
- Alert rules can use "Watchlist" (自选) as their scope.
