# Paper trading and strategies [模拟交易](/trading)

**Everything is paper trading**: no real orders are ever sent to OKX and the OKX account is never read (the "Live" (实盘) option when creating an account is greyed out and needs separate authorization). Fills are simulated against the real order book (live) or candles (backtest), including fees, slippage and funding.

Trading sub-tabs: Accounts (账户, `/trading`), Strategy library (策略库, `/trading/strategies`), Pending (待确认, `/trading/prompts`), AI decisions (AI 决策, `/trading/ai`). New account at `/trading/new`.

## Accounts

- Account = one deposit (paper USDT) + a set of hard risk rules; strategy instances attach to an account, and several strategies share its capital and risk controls.
- Types: **Live paper** (实时模拟: runs continuously on live data, can add deposits) / **Historical backtest** (历史回测: replays historical candles over a chosen start–end, max 365 days; deposit credited once at the start; report produced at the end; can rerun/cancel).
- Account page: equity curve (1D/7D/30D/All), positions, orders, fills, ledger, signals, strategy instances; can manually close one or all positions, deposit, edit risk controls, archive.
- **Equity** = cash + position margin + unrealized PnL (at mark price; in v1 mark price = last trade price); **Available** = cash not used as margin.
- **Positions**: isolated margin; liquidation price estimated at 0.5% maintenance margin rate; when mark price crosses it the whole position closes at the liquidation price and margin is lost. One direction per contract per instance.
- **Orders**: include pending stop-loss/take-profit (OCO: one fills, the other cancels). **Ledger**: deposits, fees, funding, realized PnL, liquidations.

### Risk rules (neither strategies nor AI can bypass)

| Item | Meaning |
|---|---|
| Max positions (最大持仓数) | Number of contracts held at once |
| Max leverage (最大杠杆) | Isolated leverage cap (higher requests are clipped); the system also lowers leverage so the liquidation price sits at least 25% of the stop distance beyond the stop |
| Risk per trade (单笔风险) | Fraction of equity lost if the stop is hit at signal strength 1 (1%–500%, log slider) |
| Per-contract cap (单合约上限) | Margin used by one contract ≤ this fraction of equity |
| Daily-loss circuit breaker (日亏损熔断) | Once the day's (UTC+8) realized + unrealized loss reaches this fraction of equity, no new entries for the rest of the day; resumes next day automatically; closes and stops unaffected; 0 = off |
| Cooldown (冷却时间) | Minutes after closing a contract during which the same contract can't be reopened |
| Require stop-loss (必须带止损) | Entry signals without a stop price are rejected |
| Slippage tolerance (滑点容忍) | Worst market-entry fill = mid × (1 ± bp/10000); the unfilled remainder is cancelled; triggered stops/take-profits fill at market, not limited by this |
| Fees (手续费) | taker / maker, default 5 bp / 2 bp; funding charged from real settlement records |
| Confirmation (需要确认) | Off / entries only / all (关闭 / 仅开仓 / 全部); confirmation timeout (seconds) and timeout default (skip / execute — 跳过 / 执行) |

**Sizing formula**: quantity = equity × risk per trade × signal strength × scale ÷ (|entry − stop| × contract value), then constrained by leverage and per-contract cap, rounded down to the minimum order size; below the minimum it is rejected.

## Strategy instances

Instance = a published version of strategy code + parameters + instruments + execution mode; the same strategy can be attached several times with different parameters. Changing parameters/instruments/execution mode creates a new config version (v2, v3…); past signals keep their original version.

- **Instruments**: a fixed list (≤ 50 contracts), or a dynamic screen — "Active pool by score" (top N by the chosen rule's long score / short score / stronger side, optional minimum score) or "Whole market by turnover" (crypto perps ranked by 12h turnover; scores not available). Updated at the "Rescreen" (重新筛选) interval; removed contracts get no new entries, existing positions are managed as usual. Scores are unvalidated, just a ranking basis.
- **Execution modes**:
  - **Auto** (自动): signals go straight through risk controls and sizing to an order.
  - **AI review** (AI 审核): the signal, account and positions, market summary, the strategy's recent decision outcomes and lessons document are sent to AI first; AI answers execute or skip, may shrink size and adjust stop/take-profit, but cannot enlarge size, bypass risk controls or change parameters directly. Only entry signals are reviewed. AI unavailable (unauthorized, proxy down) or timeout → treated as skip. After AI "execute", risk controls still apply and the result may be "risk rejected". In backtests AI review is treated as auto (the AI may remember history — lookahead).
  - **Log only** (只记录): only records signals and their outcome, never orders; for observing new strategies.
- Order of steps: execution mode (AI review) → manual confirmation (if enabled) → risk controls → sizing → order.
- **Status**: not started / running / paused (no new candles processed; positions and stops continue) / faulted (process timeout or repeated errors; stops after 3 automatic restarts) / stopped — 未启动 / 运行中 / 已暂停 / 故障 / 已停止. **Stopping a strategy does not close positions**; close manually if needed.
- **Signal outcomes**: ordered, risk rejected (reason in "Result" (结果)), AI skipped, user skipped, expired (waiting for confirmation or AI past TTL), log only — 已下单 / 风控拒绝 / AI 跳过 / 用户跳过 / 过期 / 只记录. Signal time is the candle's close time (exchange time).

## Manual confirmation `/trading/prompts`

For accounts with confirmation on, eligible signals wait in "Pending" (待确认) before ordering and are pushed to Telegram (Settings → Interaction channel (交互通道)). Answer in either place: **Execute** (执行: normal size, still subject to risk controls) / **Half** (半仓) / **Skip** (跳过). Timeout applies the "timeout default". Backtests don't wait for confirmation.

## AI decisions `/trading/ai`

Record of every AI review (full context, model, latency, cost). **Parameter suggestions** (参数建议): proposed by AI, take effect only after the owner approves (creating a new config version). **Lessons document** (经验文档): daily (00:05 UTC+8), for strategies with new decisions, AI reads past decisions and close outcomes and distills a short document that later reviews include; each run creates a new version, old versions kept. AI authorization is in [设置](/settings) → AI authorization (AI 授权); AI is reached only via proxy, never directly.

## Backtest report `/trading/backtests/<id>`

- Fill model: a signal fills at the next **base candle**'s open (± half spread, assumed 2 bp + 1 tick); base candle is 1H when both strategy timeframe and timer are ≥ 1H, otherwise 1m. Within one candle, stop-loss takes priority over take-profit; fees and real funding are included.
- **Net return** = ending equity − deposits (including unrealized); **Gross return** = net return + fees − funding.
- One trade = one strategy on one contract from entry to full close; PnL = realized − fees; win rate and profit/loss ratio are per trade; "—" if no trades.
- **Sharpe** = mean daily (UTC+8) equity return ÷ std × √365; not computed under 5 days. Also max drawdown, turnover, per-contract contribution (realized − fees + funding).
- Trade explorer: portfolio view (equity curve + a bubble per trade, badges 止/盈/爆/手 = stop / take-profit / liquidation / manual) and pair view (candles with each fill and stop/take-profit lines).
- A backtest only shows how this parameter set did over this history; the more tuning, the more overfitting — follow up with live paper trading out of sample.

## Strategy library [策略](/trading/strategies)

- A strategy is a Python class written with the `ctg` SDK: it declares parameters, instruments and timeframe, and returns signals on each closed candle. Strategies only emit signals; ordering, risk controls and sizing are handled by the account; strategy processes run in an isolated container without network access.
- **Built-in strategies** (read-only examples, can "Copy as new strategy" (复制为新策略)): 演示脉冲 `demo-pulse`, 大币均线趋势 `majors-trend`, 币池残差反转 `pool-reversal`, 山寨币状态趋势 `alt-regime-trend`.
- **My strategies** (我的策略): the web editor autosaves drafts; "Trial run" (试运行) replays the draft over the last 1–60 days (default 14 days, ≤ 90 s, results not kept); "Publish" (发布) creates an immutable version, required before backtesting or attaching to an account.

## Help articles (`/help/strategy/<slug>`)

| slug | Title |
|---|---|
| `quickstart` | 快速开始 (Quick start) |
| `lifecycle` | 策略结构与生命周期 (Strategy structure and lifecycle) |
| `params` | 参数声明 (Parameter declarations) |
| `ctx` | 数据接口 ctx (Data interface ctx) |
| `signals` | 信号、止损止盈与仓位计算 (Signals, stop/take-profit and sizing) |
| `short-reverse` | 做空与反手 (Shorting and reversing) |
| `portfolio` | 多币种与组合 (Multi-coin and portfolios) |
| `notify` | 发通知 (Sending notifications) |
| `backtest-trial` | 试运行与回测 (Trial runs and backtests) |
| `no-signal` | 为什么没有信号 / 信号为什么没成交 (Why no signal / why a signal didn't fill) |
| `builtins` | 内置策略讲解 (Built-in strategies explained) |
| `api` | API 参考 (API reference: all SDK classes, methods, fields) |

Example: [为什么没有信号](/help/strategy/no-signal), [内置策略讲解](/help/strategy/builtins).
