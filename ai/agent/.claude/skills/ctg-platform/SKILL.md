---
name: ctg-platform
description: How the OKX Helper (ctg) app itself works — pages, features, fields, units, scores, charts, data freshness — and how to link the owner to the right in-app page. Use when the owner asks how to use the app, what a page, number, score or chart means, where to find something, or how a value is computed, e.g. "怎么用", "这个页面", "这个数是什么意思", "在哪看", "规则分", "多头分/空头分", "战绩/IC", "因子", "事实面板", "监控池", "扫描", "清算图怎么算", "足迹/POC", "告警", "监控", "模拟交易", "回测", "策略", "AI 审核", "数据多久更新". Not for market analysis or trading calls (use perp-analysis).
---

# OKX Helper (ctg) platform guide

OKX Helper is the owner's personal **OKX USDT-margined perpetual research desk**: it finds liquid contracts in real time (the monitored pool), shows each coin's state objectively (the facts panel), scores them with the owner's own rules, and automatically tests each rule against subsequent real prices (track record). It uses OKX **public** market data, plus — read-only, when the owner configured a key — his own open OKX positions (entry, liquidation price and TP/SL on the K-line; MCP `get_positions`). It places a real order only when the owner approves an AI trade proposal (`propose_trade`: market + TP/SL, isolated; dry run until he switches it to live) — never otherwise; everything on the trading pages is paper trading. A score is a rule's ranking, not a win rate. UI times default to UTC+8.

## Pages

| Page | In-app path | Summary |
|---|---|---|
| Scanner (扫描) | `/` | Pool list: long/short scores, funding, price change, OI change, volume surprise, market cap, 12h turnover; search, filters, watchlist; on PC the contract workspace is on the right |
| Instrument (合约详情) | `/instrument/<instId>` | Price and key numbers, score card, candle chart (Delta/footprint/spot/OI/liquidations/liquidation map), order book, relative to BTC/ETH, OI and funding history, liquidations |
| Rules & track record (规则与战绩) | `/rules`, `/rules/<ruleId>` | Edit long/short weights and spread gate (each save = new version); Rank IC, t-stat, top-20% excess/net return |
| Alerts (监控告警) | `/alerts` | Three alert rule types — price move / rule score / funding — pushed via Telegram / Webhook; recent triggers |
| Paper trading (模拟交易) | `/trading` | Paper accounts (live paper, historical backtest), risk controls, strategy instances, fills and reports |
| Strategy library (策略库) | `/trading/strategies` | Built-in and custom Python strategies; edit, publish, backtest |
| Pending / AI decisions (待确认 / AI 决策) | `/trading/prompts`, `/trading/ai` | Signals awaiting manual confirmation; every AI-review decision, parameter suggestions, lessons documents |
| System status (系统状态) | `/status` | Phase, data freshness, coverage and gaps, OKX connection, storage and retention, background job progress |
| Settings (设置) | `/settings` | Pool threshold/cap/categories, default rule, storage cap, time zone, open-interest bar, developer, interaction channel, AI authorization |
| Help (帮助) | `/help/strategy/<slug>` | Strategy-writing help articles (see paper-trading-and-strategies.md) |

Mobile bottom bar: Scanner (扫描), Rules (规则), Trading (交易), Settings (设置) + "More" (更多: alerts, help, system, etc.). On PC, opening `/instrument/<instId>` redirects to the Scanner split workspace (`/?sel=<instId>`); the link still works.

## In-app links (mandatory)

The chat renders Markdown links **whose target is an in-app path** as tappable buttons that navigate inside the app. Whenever you mention something the owner can open, write it as such a link:

- Contract (always the full instId): `[SOL](/instrument/SOL-USDT-SWAP)`
- Rule and its track record: `[残差反转](/rules/resid-reversal)`, i.e. `[规则名](/rules/<ruleId>)` (look up ruleId with MCP `list_rules`)
- Pages: `[扫描](/)`, `[规则与战绩](/rules)`, `[监控告警](/alerts)`, `[模拟交易](/trading)`, `[策略](/trading/strategies)`, `[系统状态](/status)`, `[设置](/settings)`, `[帮助](/help/strategy/<slug>)`

Use only these app paths; external sources stay plain https links. Example: when recommending 3 coins, write each coin name as an `/instrument/...` link; when saying "check the track record", give the rule link directly.

## Answering rules

- Check live definitions before concluding: fields via MCP `list_fields`, rules via `list_rules`, track record via `get_rule_track_record`, data freshness via `get_system_status`. The reference files explain how things are computed and read; actual values come from the tools.
- Missing = unknown (shown as "—" / unscored); never read it as 0. When citing a rule score, state whether it is backed by a significant out-of-sample track record (Rank IC, t-stat).
- Don't invent features you're unsure of; saying "the app doesn't have that" beats guessing.

## Reference files (read as needed)

| File | When to read |
|---|---|
| [references/pool-and-scanner.md](references/pool-and-scanner.md) | How the pool is defined, why a coin isn't in / was removed from the pool, Scanner columns/filters/sorting, watchlist, top-bar status |
| [references/facts-and-scores.md](references/facts-and-scores.md) | Facts-panel fields and units (log returns, funding per hour, etc.), percentiles, rule-score formula, gate, unscored, how to read the track record (Rank IC, t-stat, net return), built-in baseline rules |
| [references/instrument-page.md](references/instrument-page.md) | Instrument page sections; chart layers, timeframes, spot/OI Δ/OI/liquidations, footprint, POC/value area/imbalance, liquidation-map model and how it differs from CoinGlass, refresh rates; order book and depth chart |
| [references/alerts.md](references/alerts.md) | Alert rule types and parameters, triggering and cooldown, push channels, trigger history |
| [references/paper-trading-and-strategies.md](references/paper-trading-and-strategies.md) | Paper accounts, risk controls and sizing formula, strategy instances and execution modes (auto / AI review / log only), manual confirmation, backtest report metrics, strategy library, help-article slugs |
| [references/data-sources-and-freshness.md](references/data-sources-and-freshness.md) | What is recorded locally vs fetched on demand, retention and tiers, startup phases, time zones, System status health checks |
