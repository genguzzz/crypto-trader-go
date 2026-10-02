# Trading design (strategies, paper trading, interaction, AI)

Implements [#3](https://github.com/genguzzz/crypto-trader-go/issues/3). Owner-approved proposal (Chinese): Claude Docs artifact "crypto-trader-go 策略与模拟盘重构方案" (2026-10-02). Shared Go types: `internal/trade/contract.go`.

Scope of this release: strategy SDK + runtime, order layer with a simulated broker, live paper accounts and backtest accounts, a two-way interaction layer (Telegram first), an optional AI review layer, and the web UI. **No real orders, no OKX account access**: `ModeReal` is rejected.

## 1. Layers and packages

| Package / dir | Owns | Talks to |
|---|---|---|
| `internal/trade` | accounts, instances, signals, risk, sizing, orders, SimBroker (live + bar model), positions, ledger, funding, liquidation, backtest replayer, its SQLite tables, REST handlers `/api/v1/trade/*` | `MarketData`, `Interactor`, `Reviewer` |
| `internal/trade/mdlive` | `MarketData` adapter over `market.Engine` | `internal/market` |
| `internal/strategyhost` | Unix-socket server for strategy processes, per-instance sessions, lockstep event delivery, data queries | `trade.SignalSink`, `trade.MarketData` |
| `strategies/` | Python SDK (`strategies/ctg/`), supervisor (`strategies/runner.py`), example strategies (`strategies/library/*.py`), Dockerfile | strategyhost socket |
| `internal/interact` | `Hub` (implements `trade.Interactor`), Telegram channel (long polling, inline keyboards, commands, owner allowlist), web channel (pending prompts over REST/SSE), outbox, its tables, REST `/api/v1/interact/*` | Telegram Bot API |
| `internal/aigate` | `Reviewer` implementation, credential storage (masked), decision queue served to the AI worker over a Unix socket, audit table, memory docs, REST `/api/v1/ai/*` | AI worker |
| `ai/` | Python AI worker (Claude Agent SDK), Dockerfile | aigate socket, Anthropic API |
| `web/src/views/Trading*.vue` etc. | accounts, wizard, account detail, strategy library, backtest report, pending confirmations, AI auth settings | REST |

Each Go package creates its own tables (`CREATE TABLE IF NOT EXISTS` in its constructor) on the shared `*store.Store` DB; no edits to `internal/store/store.go` schema. Trading records are append-only except `state` columns of orders/instances.

## 2. Strategy process protocol (NDJSON over Unix socket)

Socket: `$CTG_RUN_DIR/strategy.sock` (default `data/run/strategy.sock`; in Docker `/run/ctg/strategy.sock` shared by a volume). One connection per strategy instance process. Every message is one JSON object per line. Go → Python messages carry `seq`; Python must answer each event with `{"type":"done","seq":N}` (lockstep). Queries may be sent at any time between receiving an event and sending `done`; they are answered synchronously with matching `qid`.

Handshake (Python → Go first line): `{"type":"hello","instance":12,"token":"<per-instance token>","sdk":"1"}`. Go answers `{"type":"init","seq":0,"instance":{...Instance},"strategy":{...StrategyVersion without code},"state":{...saved ctx.state},"now":ms,"mode":"live|backtest"}`; Python replies `done` after `on_start`.

Go → Python events:

| type | fields |
|---|---|
| `bar` | `instId`, `tf`, `bar` {ts,o,h,l,c,vol,volq}, `now` — one per instrument in the universe per confirmed bar of the instance timeframe |
| `universe` | `added` [], `removed` [], `current` [] |
| `fill` | `fill` {Fill} |
| `signal_result` | `signalId`, `outcome`, `reason` |
| `funding` | `instId`, `rate`, `amount` |
| `stop` | — (process should exit after `done`) |

Python → Go:

| type | fields | answer |
|---|---|---|
| `signal` | Signal fields (`instId`, `action`, `strength`, `stopLoss`, `takeProfit`, `maxSlippageBp`, `leverage`, `reduceFrac`, `ttlMs`, `reason`) | `{"type":"signal_ack","qid","id"}` or `error` |
| `query` | `qid`, `method`, `args` | `{"type":"result","qid","data"}` or `{"type":"error","qid","error"}` |
| `state` | `data` (JSON object ≤ 64 KB) | none (persisted) |
| `log` | `level`, `msg` | none |
| `done` | `seq` | — |

Query methods: `bars {instId, tf, n}` → [[ts,o,h,l,c,vol,volq]...] oldest first · `factors {instId}` → map · `screener {ScreenerQuery}` → [ScreenRow] · `score {rule, instId}` → {long, short} · `position {instId}` → Position|null · `positions {}` · `account {}` · `spec {instId}`.

Timeouts: an event not acknowledged within 5 s → kill the process, instance `faulted`; supervisor restarts it up to 3 times (state restored), then `stopped`. Python exceptions inside a handler are logged and the event is acknowledged; 3 consecutive → `paused`.

The supervisor (`strategies/runner.py`) polls `GET` over the same socket: `{"type":"supervise"}` connection receives `{"type":"spawn","instance":12,"token":"...","file":"library/majors_trend.py","class":"MajorsTrend"}` and `{"type":"kill","instance":12}` commands, and reports `{"type":"exited","instance":12,"code":1,"stderr":"tail"}`. One subprocess per instance (memory limit via `resource.setrlimit`, 256 MB).

Backtest: the replayer spawns the instance like a live one (mode `backtest`); events are sent as fast as `done` comes back.

### Python SDK surface (`from ctg import Strategy, Param, Screener, Signal, ta`)

```python
class MajorsTrend(Strategy):
    name = "majors-trend"; description = "..."
    universe = ["BTC-USDT-SWAP", "ETH-USDT-SWAP"]   # or Screener(rule="...", side="long", top=20, refresh="1H")
    timeframe = "4H"
    fast = Param(20, min=5, max=100, desc="...", ai_tunable=True)
    def on_start(self, ctx): ...
    def on_bar(self, ctx, bar): return Signal.open_long(bar.inst, stop_loss=..., take_profit=..., reason="...")
    def on_universe_change(self, ctx, added, removed): ...
    def on_fill(self, ctx, fill): ...
    def on_signal_result(self, ctx, result): ...
```

`on_bar` may return a Signal, a list of Signals or None. `stop_loss` / `take_profit` accept a price or a string `"2atr"` (SDK resolves with ATR(14) on the strategy timeframe). `ctx.bars(inst, tf, n)` returns a small columnar object (`.o .h .l .c .vol .ts` numpy arrays). `ctx.state` is a dict persisted after each event. `ta` mirrors Pine names (`ema sma rsi atr macd bbands crossover crossunder highest lowest`) implemented with numpy only (no TA-Lib dependency).

Strategy discovery: Go scans `strategies/library/*.py` metadata through the supervisor (`{"type":"describe"}` → list of {file, class, name, description, timeframe, params, universe, code}) at start and on `POST /api/v1/trade/strategies/reload`; each distinct code hash becomes a `StrategyVersion`.

## 3. Order layer rules

Pipeline per signal: validate (TTL, instance running, no pending signal for the same instrument, spec exists) → exec mode (record → `recorded`; ai → `Reviewer.Review` with ctx deadline = TTL; unavailable → account `ConfirmDefault`-like fallback: skip) → human confirm if `Limits.Confirm` matches (Interactor.Ask; AI rationale included) → risk (max positions, leverage cap, max instrument margin, daily loss breaker, cooldown, require stop) → sizing (`qty = equity × RiskPct × strength × sizeScale / (|entry − stop| × ctVal)`, capped by leverage/margin/MaxInstPct, rounded down to lot, ≥ minSz else reject) → order group (entry market/IOC + stop + target, OCO, reduce-only) → SimBroker.

SimBroker:
- Live: market/IOC walks `Quote.Asks/Bids` up to the worst price `mid × (1 ± bp/1e4)`; rest cancelled. No depth → `Last ± max(halfSpread, 1 tick)` plus 1 tick. Limit fills when price trades through. Stops/targets are checked on every price tick (≤ 2 s polling of `Quote` for instruments with open orders) and fill as market without the slippage cap.
- Backtest: market fills at next bar open ± half spread (from the 1m spread factor if known, else 2 bp) + 1 tick; within one bar, stop has priority over target; triggers use bar high/low.
- Fees taker/maker from limits; funding charged at settlement times from recorded funding samples (`FundingRate`); isolated margin = notional / leverage; maintenance margin rate 0.5% flat in v1 (TODO tiers); liquidation when mark crosses liq price → whole position closed at liq price, margin lost, `liquidation` ledger entry.

Daily breaker: day boundary UTC+8; when tripped, opening signals are rejected until the next day and an `breaker` event is sent.

## 4. REST API (all JSON, under `/api/v1`)

Trade (`internal/trade`):
- `GET trade/accounts` · `POST trade/accounts` {name, mode, deposit, limits, from?, to?} · `GET trade/accounts/{id}` (account + positions + open orders) · `PATCH trade/accounts/{id}` {name?, limits?, archived?} · `POST trade/accounts/{id}/deposit` {amount}
- `GET trade/accounts/{id}/equity?from&to` → [[ts, equity]] (1m live / bar backtest, downsampled ≤ 2000 points)
- `GET trade/accounts/{id}/fills|orders|signals|ledger?limit&before`
- `POST trade/accounts/{id}/close` {instId?} (manual close one or all) · `POST trade/backtests/{id}/run` (re-run)
- `GET trade/accounts/{id}/report` → {gross, net, maxDrawdown, sharpe, winRate, profitFactor, turnover, fees, funding, trades, byInst[], signals{ordered,rejected,skipped}}
- `GET trade/strategies` (latest version per name + versions list) · `GET trade/strategies/{versionId}` (with code) · `POST trade/strategies/reload`
- `GET trade/accounts/{id}/instances` · `POST trade/accounts/{id}/instances` {versionId, params, universe, exec} · `PATCH trade/instances/{id}` {params?, universe?, exec?} (new config version) · `POST trade/instances/{id}/{start|pause|resume|stop}`
- SSE: existing `/api/v1/stream` gets event types `trade` (account id + kind) so pages refetch.

Interaction (`internal/interact`): `GET interact/prompts?state=open` · `POST interact/prompts/{id}` {choice} · `GET|PUT interact/config` (telegram token masked, allowlist user ids, enabled kinds) · `POST interact/test`.

AI (`internal/aigate`): `GET ai/status` {configured, kind: api_key|oauth_token, masked, workerOnline, lastCallAt, lastError, costToday} · `PUT ai/credential` {kind, value} · `DELETE ai/credential` · `POST ai/test` · `GET ai/decisions?accountId&limit` · `GET|POST ai/proposals` (parameter change proposals; `POST ai/proposals/{id}` {approve: bool}).

## 5. AI worker protocol (NDJSON over Unix socket)

Socket `$CTG_RUN_DIR/ai.sock`. Worker connects, sends `{"type":"hello","worker":"1"}`; Go sends `{"type":"config","credential":{"kind","value"},"model":"..."}` (and again when changed), then `{"type":"review","id":"<uuid>","request":{ReviewRequest},"deadlineMs":...}`; worker answers `{"type":"decision","id","decision":{Decision without id}}` or `{"type":"error","id","error"}`. `{"type":"test","id"}` → a minimal call to verify the credential. Heartbeat `{"type":"ping"}`/`pong` every 15 s.

The worker uses `claude_agent_sdk.query` with no built-in tools (`allowed_tools` = only the in-process MCP tools `decide`, `skip`), a system prompt describing the role and hard limits, and the request rendered as a compact context pack; credential via env `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` set per call. No claude.ai OAuth flow in the UI (Agent SDK terms); the owner pastes an API key or a token from their own `claude setup-token`.

## 6. Deployment (ctg-prod)

`/opt/ctg/docker-compose.yml` adds two services built from `strategies/Dockerfile` and `ai/Dockerfile` (`python:3.12-slim`), sharing a `run` volume for the sockets with `ctg`. `ctg-strategy`: `network_mode: none`, read-only rootfs, `mem_limit: 768m`, strategies dir bind-mounted read-only. `ctg-ai`: bridge network (Anthropic API), `mem_limit: 512m`. `deploy/deploy.sh` uploads `strategies/` and `ai/` and runs `docker compose build` only when their content hash changed.
