# Instrument page and price chart

Link: `[SOL](/instrument/SOL-USDT-SWAP)`. On mobile it's a standalone page; on PC it redirects to the Scanner split workspace (list on the left, chart in the middle, toggleable side panel), same content.

## Page layout (mobile, top to bottom)

1. **Header**: coin/USDT, category, quote update time; status tags (live / data gap, backfilling / warming up / listed < 12h — 实时 / 数据缺口，补洞中 / 预热中 / 上市不足 12h), `活跃 #rank` (active) or `非活跃` (inactive: below threshold or incomplete data) or `未录制` (not recorded: outside categories/pre-market; recorded live while viewing, kept long-term once on the watchlist), `同步中` (syncing: downloading history); watchlist button.
2. **Price**: last price, 24h and 4h change. The PC workspace also shows mark price deviation from index price.
3. **Key numbers** (tap for a detail popup):
   - `资金费 Nh` (funding): current funding (single period, %), settlement interval, countdown to next settlement. Details include per-hour equivalent and simple annualized (extrapolated from this period only, not a forecast).
   - `持仓量` (open interest): USD OI + 4h coin-denominated OI change.
   - `12h 额` (12h turnover): 12h USDT turnover + volume-surprise multiple; details aggregate by hour to show whether volume is concentrated.
4. **Cards**:
   - `评分` (score): long/short score for the current rule; subtitle is the rule name, shows "门槛：…" (gate) if the gate fails, "预热中" (warming up) if no score yet. Details can switch rules: each side lists every weighted field's value, pool percentile and contribution to the score (relative to 50); missing fields read "缺少：… → 未评分（不是 0）" (missing → unscored, not 0); the "All fields" (全部字段) table below lists every facts-panel field's value, percentile and definition, with non-replayable fields marked.
   - `总市值` (market cap): CoinGecko market cap and circulating ratio (refreshed ~every 2 hours; matched by symbol taking the largest-cap coin of that name, so a few small coins may be mismatched; display only, not used for pool entry or scoring; no unlock schedule).
   - `买一 / 卖一` (best bid / ask): spread in bps; details show the order book and depth chart.
   - `OKX 现货` (OKX spot): same-name USDT spot price and basis (perp ÷ spot − 1); without spot shows "无同名 USDT 现货" (no same-name USDT spot).
5. **Price chart** (next section).
6. **Live order book**: OKX books-full, up to 5000 levels per side (BTC covers about ±1%, small coins up to tens of %), refreshed every second, amounts in USDT (price × contracts × contract value). The depth chart's x-axis is % from mid, y-axis cumulative resting USDT (default the smaller of ±5% and coverage); hover/drag shows how much USDT must be eaten to push price to that offset, average fill price and slippage. A "+" in the quick table means beyond the public book, actually more. Excludes hidden/iceberg orders; cancellations change it quickly.
7. **Relative to BTC/ETH**: index of the three perps' closes from a common start = 100; 105 = +5% since start. BTC/ETH are just market proxies.
8. **OI and funding history**: OI in oiCcy (coins); data = OKX 1H history + last 8h of 5m + local per-minute recording. Funding bars are each settled rate (single period %).
9. **Liquidations**: OKX public liquidation orders (liquidation-orders), amount = bankruptcy price × contracts × contract value (USDT). Long = longs liquidated (red bar down), short = shorts liquidated (green bar up). OKX keeps only ~24h; contracts in active pool/watchlist/recently viewed sync every minute and are kept 30 days locally; windows starting before the recording start are marked "incomplete" (不完整). "Latest liquidations" (最新强平) lists the last 20, top 10% by amount in bold.
10. **News / unlocks / on-chain holders**: not integrated (planned). "Not integrated" (未接入) doesn't mean there are no events or risks.

## Price chart

Toolbar: layers `K 线` (candles) / `Delta` / `足迹` (footprint); timeframes `1m` `5m` `15m` (default) `1H` `4H`; overlay toggles `现货` (spot) `OI Δ` (default on) `强平` (liquidations, default on) `清算图` (liquidation map, default off) `OI` (default off); fullscreen. Settings are remembered in this browser.

- **Data**: perp 1m candles recorded locally; 5m/15m/4H aggregated server-side aligned to UTC (4H at 0/4/8/12/16/20 in UTC+8). Lookback on the chart roughly: 1m 1 day, 5m/15m 7 days, 1H/4H 30 days. Missing minutes show as gaps; the last candle is still open.
- **Crosshair**: top-left shows OHLC, change (vs previous close), range, turnover; the right price label shows % from last price. On mobile, long-press then drag. Bottom-right ◎ or double-click returns to latest.
- **Turnover bars**: USDT turnover at the bottom, green up, red down.
- **Spot**: OKX same-name USDT spot close (yellow line) on the same price axis; the gap is the basis.
- **OI Δ**: per-candle change in OI vs previous candle (blue up, orange down). **OI**: OI area chart. Recent data is per-minute samples, older only hourly, so small timeframes show no change bars for that period.
- **Liquidation bars**: liquidation USDT summed per candle (green up = shorts liquidated, red down = longs liquidated); periods without records are not drawn.

### Order flow (Delta layer)

Taker buy / taker sell USDT turnover. **Delta** = buy − sell (bars), **CVD** = cumulative Delta over the range (yellow line, independent scale). Candles from local trade recording are dark; older ones are filled from OKX's 5m/1H taker buy/sell stats (light, unavailable on 1m). Trades are recorded only for active pool, watchlist and recently viewed contracts.

### Footprint (footprint layer)

Per price level within each candle: taker sell (left, red) / taker buy (right, green); shade scales to the largest single-level turnover in the visible range. Levels merge automatically with vertical zoom; tick size shown top-left; drag the right price axis to zoom into finer levels; numbers appear when wide enough. Footprint is drawn only if every minute of the candle is complete; opening the page auto-backfills missing minutes within retention (whole days from OKX daily trade files, split by Beijing time and published next day; today from the historical trades endpoint).

- **POC**: the candle's highest-volume price level (yellow box). **Value area**: range expanded from POC both ways to cover 70% of turnover (thin gold line left of the candle).
- **Naked POC** (未回补 POC): a closed candle's POC not crossed by any later candle's high–low range, drawn as a gold dashed line to the right edge (max 5); disappears once crossed.
- **Imbalance** (diagonal comparison): taker buy at a level ≥ 3× taker sell one level lower (green bar on the right), or taker sell ≥ 3× taker buy one level higher (red bar on the left); levels below the candle's one-side median volume don't count. ≥ 3 consecutive same-direction levels = **stacked imbalance** (outlined).
- **How to read**: green candle with POC near the top = buyers absorbing high, strong; green candle with POC low and thin volume in the upper wick = rally without follow-through. Heavy taker buying at the range high that fails to lift price (absorption) or volume drying up at new highs (exhaustion) are common reversal clues; stacked-imbalance levels often act as support/resistance. These describe what happened, not predictions.

### Liquidation map (estimated)

Not exchange data; same model family as CoinGlass's liquidation map:
1. Infer new positions from OI **increases**: recent data uses 5m candles + per-minute OI (contiguous data, `fine`), older uses hourly.
2. New positions open at the segment's typical price, split across leverage 5/10/25/50/100x (assumed shares 10/25/30/20/15%; tiers above the contract's max leverage dropped), placed at their approximate liquidation price (maintenance margin rate 0.5%).
3. OI decreases shrink positions proportionally; levels later crossed by candle prices are treated as liquidated and removed.

Display: horizontal bars right of the price area = remaining estimated liquidation amount per level; red = longs (below price), green = shorts (above price), brighter = higher leverage; thin line = cumulative amount outward from price. Lookback switchable 1 / 7 days (default 7), can show only some leverage tiers (CoinGlass model 1 uses only 10/25/50/100x). Bar length is relative to the largest row near the current price. Hovering a level shows the cumulative liquidations expected if price reaches it, and the amount per leverage tier.

**Numbers differing from CoinGlass is normal**: CoinGlass aggregates multiple exchanges by default, its leverage shares and inputs (including volume) are its own assumptions, and bar height is relative strength, not USDT; this map uses only this one OKX contract's OI and amounts are USDT estimates. Compare where positions cluster, not absolute numbers. It shows only where positions may cluster, not real positions. MCP equivalent: `get_liquidation_map` (days=1|7).

### Refresh

With the instrument page open, the latest candles refresh every second (open candle pushed via OKX WebSocket); full data, order flow, liquidation bars (fetching new liquidation orders from OKX each time) and the liquidation map (with latest OI and open candle) refresh every 10 s; paused while the page is in the background. Times use the time zone from Settings (default UTC+8).
