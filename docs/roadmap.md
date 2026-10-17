# Roadmap

Planned work lives in GitHub issues, tracked by the pinned **[Roadmap issue #6](https://github.com/genguzzz/crypto-trader-go/issues/6)**. Anyone can pick up an unclaimed item — see [claiming work](contributing.md#claiming-work). This file records what has shipped.

## Planned

| Priority | Issue | Status |
|---|---|---|
| P1 | [#1 Authentication and access control](https://github.com/genguzzz/crypto-trader-go/issues/1) | needs design |
| P1 | [#2 Monitoring: complete alert conditions, delivery and noise control](https://github.com/genguzzz/crypto-trader-go/issues/2) | needs design |
| P2 | [#3 Strategy system with backtesting and paper trading](https://github.com/genguzzz/crypto-trader-go/issues/3) | first release on the test server ([design](trading-design.md)) |
| P2 | [#4 Agent access: documented API and MCP server](https://github.com/genguzzz/crypto-trader-go/issues/4) | shipped (key-authenticated); owner UI login still depends on #1 |
| P3 | [#5 Internationalization: English and Chinese UI](https://github.com/genguzzz/crypto-trader-go/issues/5) | ready |

The backlog of smaller, not yet filed items is listed in the Roadmap issue. Issue labels are the source of truth for status.

## Shipped

| Date | Milestone |
|---|---|
| 2026-09-19 | Research and end-to-end plan (superseded by [decisions](decisions.md)) |
| 2026-09-26 | Plan revised with measured public-API data: pool size, stock/commodity perps, ticker bandwidth, score direction falsified |
| 2026-09-26 | I0–I3 first release: universe recording, per-minute pool, fact panel, rules and track record, Vue UI |
| 2026-09-27 | 10 s intraminute preview; 30-day 1H depth, 14-day replay, 7-day 1m backfill; coin icons; Settings page (threshold default 5M USDT, N, categories, default rule); metric cards with detail views (live order book, USDT depth chart with price-impact cost, turnover / OI / funding / basis) |
| 2026-09-27 | UI overhaul: design tokens and information hierarchy; explanations moved into InfoTip/Modal; Reka UI component library; new navigation with a phone tab bar; single scroll area on PC; no horizontal overflow on phones; instant Settings and gzip API ([guidelines](design-guidelines.md)) |
| 2026-09-27 | Search covers every OKX USDT perp (in-memory catalog, history downloaded on first open); tiered retention with per-table retention and quotas, 2 GB default cap |
| 2026-09-27 | Production on Docker (distroless runtime + mounted binary, `deploy/deploy.sh` upgrade/rollback) at https://ctg.gugenzzz.top |
| 2026-09-27 | K-line order flow: delta/CVD (local trades + OKX rubik taker volume) and footprint; crosshair details; market cap / circulating share (CoinGecko); tap stability on phones; bottom-sheet dialogs |
| 2026-09-27 | Footprint: shared level step with zoom-based merging, POC / 70% value area / diagonal and stacked imbalances / naked POCs; detail header reorganized so the chart is visible on the first screen |
| 2026-09-27 | Server moved from zouter to greencloud (GreenCloud Tokyo 2C/4G); database migrated; DNS and CI secrets repointed |
| 2026-09-28 | Display time zone (default UTC+8); per-second updates of the open candle; spot/OI overlay toggles; full-depth order book (5000 levels per side) |
| 2026-09-28 | Renamed OKX Helper with new icon and PWA manifest; OKX palette; chart view anchored across layer/period switches; loading states |
| 2026-09-28 | PC workbench layout: collapsible contract list, chart-first main area, IDE-style side panels, resizable splits, K-line fullscreen |
| 2026-09-28 | Depth chart fixed at ±5%; trend-signal research ([report](research-trend-signals.md)): β-neutral residual returns, EMA trend, efficiency ratio, 15m fields and three reference rules; default rule "Residual reversal (β-neutral)" |
| 2026-09-28 | System and Settings pages redone: health summary, section navigation, storage details on System; grouped settings with unsaved count, undo and inline validation |
| 2026-09-28 | Monitoring tab: price move, score and funding alert rules evaluated each confirmed minute, Telegram/webhook delivery, history (open questions moved to [#2](https://github.com/genguzzz/crypto-trader-go/issues/2)) |
| 2026-09-28 | Liquidations: OKX public liquidation orders recorded per minute for the order-flow set (30 days); K-line liquidation band, liquidation panel, estimated liquidation map overlay (hourly OI increments over 5–100x leverage tiers). Paid heatmap APIs and the throttled WS channel were rejected |
| 2026-09-29 | Scanner list: sort-direction toggle; funding column moved forward on PC and shown on phone cards (graded colors); open-interest bar as the row background (log-logistic scale, configurable half point) |
| 2026-09-29 | English documentation, contribution conventions, worktree + pull request workflow, issue-based roadmap |
| 2026-09-29 | Agent access: API keys and short-lived tokens (hashed, shown once), per-key token-bucket limits with endpoint costs and a server-wide ceiling, read-only REST API (`/api/open/v1`, OpenAPI document) and MCP server (`/mcp`) from one endpoint registry; Dev tab (keys, docs, tester, request log) and Settings → Developer switches |
| 2026-09-29 | Liquidation map closer to CoinGlass: 5m steps with per-minute OI (hourly only where data is broken), bars stacked by leverage tier (ordinal red/green ramps) with tier filter and 1/7-day look-back, per-tier crosshair readout; map and liquidation bars refresh every 10 s on an open detail page |
| 2026-10-02 | Paper trading (#3): Python strategy SDK and runner (static or screener universes, Pine-like `ta`), Go order layer with risk, sizing, OCO stops/targets and a simulated broker (live order book / bar model), backtest accounts sharing the same code, two-way Telegram interaction with confirmations and commands (dedicated bot, owner only), optional AI review via the Claude Agent SDK with audit, memory and parameter proposals, trading UI; owner key for trading writes until #1 |
| 2026-10-10 | Altcoin strategy `alt-regime-trend` (regime-gated breakouts + weak-alt shorts, owner sizing with equity-based suggested margin), market-wide turnover screener (`scope="market"`, top ≤ 300), offline multi-coin backtest `research/altbt`; research in [research-alt-perps](research-alt-perps.md) (#8) |
| 2026-10-17 | Faster instrument detail (#15): OI series sent at the chart's bar resolution (15m chart: ~630 KB → ~40 KB raw, 164 KB → 12 KB gzip), candles paint before the overlays arrive |

## Rules

- New work starts as an issue ([requirement conventions](contributing.md#requirements)); add it to the Roadmap issue.
- Changing a definition (pool rules, field formulas, scoring semantics), adding a data source or touching account permissions needs the owner's confirmation first.
- When a change ships, add a row above in the same pull request.
