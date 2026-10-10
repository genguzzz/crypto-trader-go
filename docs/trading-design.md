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

Handshake (Python → Go first line): `{"type":"hello","instance":12,"token":"<per-instance token>","sdk":"2"}`. Go answers `{"type":"init","seq":0,"instance":{...Instance},"strategy":{...StrategyVersion without code},"state":{...saved ctx.state},"now":ms,"mode":"live|backtest","timeframe":"4H"}` (`timeframe` = the instance's, a `Timeframe` param overrides the class default); Python replies `done` after `on_start`.

Go → Python events:

| type | fields |
|---|---|
| `bars` | `tf`, `bars` [{instId, bar {ts,o,h,l,c,vol,volq}}], `now` — one per bar time with every universe instrument whose bar of the instance timeframe closed (gaps left out); the SDK calls `on_bars` once or `on_bar` per instrument. Budget: 5 s + 0.5 s per instrument |
| `timer` | `name` — a timer from the class `schedule` ({name: "15m"}) is due (UTC-aligned) |
| `universe` | `added` [], `removed` [], `current` [] |
| `fill` | `fill` {Fill + `kind` (fill/stop/target/time/liquidation) + `closed` (flattened the position → `on_position_closed`)} |
| `signal_result` | `signalId`, `outcome`, `reason` |
| `funding` | `instId`, `rate`, `amount` |
| `stop` | — (process should exit after `done`) |

Python → Go:

| type | fields | answer |
|---|---|---|
| `signal` | Signal fields (`instId`, `action`, `strength`, `stopLoss`, `takeProfit`, `maxSlippageBp`, `leverage`, `reduceFrac`, `ttlMs`, `reverse`, `trailDist`, `timeLimitMs`, `reason`) | `{"type":"signal_ack","qid","id"}` or `error` |
| `query` | `qid`, `method`, `args` | `{"type":"result","qid","data"}` or `{"type":"error","qid","error"}` |
| `state` | `data` (JSON object ≤ 64 KB) | none (persisted) |
| `log` | `level`, `msg` | none (server log + the instance's run log, `GET /trade/instances/{id}/logs`) |
| `notify` | `level`, `text`, `data` | none (live accounts: event kind `strategy` to Telegram / webhook; 30/h per instance, same text once per 10 min; otherwise run log only) |
| `status` | `msg` — the strategy's `status()` line, sent after an event when it changed | none (shown on the instance) |
| `done` | `seq` | — |

Query methods: `bars {instId, tf, n}` → [[ts,o,h,l,c,vol,volq]...] oldest first · `factors {instId}` → map · `screener {ScreenerQuery}` → [ScreenRow] · `score {rule, instId}` → {long, short} · `position {instId}` → Position|null · `positions {}` · `account {}` · `spec {instId}` · `series {instId, field, tf, n}` → [{ts, v}] for oi / funding / buy / sell / delta, as of the instance clock.

Universes: a static list (≤ 50 instIds) or a `ScreenerQuery` {scope, rule, side, top, minScore, filter, refresh} re-evaluated every `refresh`. Scope `""`/`pool` ranks the active pool by the rule's score on `side` (else by pool rank), top 1–50. Scope `market` ranks every recorded (eligible) crypto USDT swap (instCategory 1; stocks, commodities and indices excluded) by 12h USDT turnover, descending, `rank` = position, top 1–300; a rule or minScore is rejected (scores exist only for pool rows), `filter` still applies (replay rows carry fact-panel `values` only when a filter is set; live rows always do — do not read `values` from a market screener in a strategy meant to be backtested; a replay `filter` sees only the replayable fact-panel fields, so live-only fields such as `spread_bps` or `basis` match nothing there). A gap in the stored 1H history (fewer than half of the last ranking left) keeps the last ranking for up to a day. Live uses the confirmed snapshot's `Turnover12h` (full 720-minute window, or the provisional value while warming; new and stale instruments are left out). Backtests sum `candles_1h.quote` over the 12 complete hours before the clock in one grouped query, leaving out instruments missing any hour and instruments without a current spec (delisted: slight survivorship bias); no data at all fails the run. The pool definition is unchanged. Large universes: a bar batch gets 5 s + 0.5 s per instrument (≈ 155 s at 300); live `Keep` is batched into one stream-subscription update; sub-1H backtests load 1m history per universe instrument (memory grows with universe × range).

Timeouts: an event not acknowledged within 5 s → kill the process, instance `faulted`; supervisor restarts it up to 3 times (state restored), then `stopped`. Python exceptions inside a handler are logged and the event is acknowledged; 3 consecutive → `paused`.

The supervisor (`strategies/runner.py`) polls `GET` over the same socket: `{"type":"supervise"}` connection receives `{"type":"spawn","instance":12,"token":"...","file":"library/majors_trend.py","class":"MajorsTrend"}` and `{"type":"kill","instance":12}` commands, and reports `{"type":"exited","instance":12,"code":1,"stderr":"tail"}`. `{"type":"check","id","code"}` runs `ctg.check` on editor code in a throwaway process → `{"type":"check_result","id","result":{diagnostics, strategies}}`. One subprocess per instance (memory limit via `resource.setrlimit`, 256 MB).

Backtest: the replayer spawns the instance like a live one (mode `backtest`); events are sent as fast as `done` comes back.

### Python SDK surface (SDK 2; full reference in `strategies/README.md` and the in-app help `/help/strategy`)

```python
class MajorsTrend(Strategy):
    name = "majors-trend"; description = "..."
    universe = ["BTC-USDT-SWAP", "ETH-USDT-SWAP"]   # or Screener(rule="...", side="long|short|both", top=20, refresh="1H"), or Screener(scope="market", top=200)
    timeframe = Timeframe("4H")                     # or a plain "4H"
    can_long = can_short = True; warmup = 150; schedule = {"report": "1H"}
    fast = Int(20, min=5, max=100, group="信号", label="快线", desc="...", ai_tunable=True)   # also Float, Bool, Choice
    def on_bar(self, ctx, bar): return Signal.open(bar.inst, "long", stop_loss="2atr", trailing_stop="1.5atr", time_limit="6h")
    def on_bars(self, ctx, bars): ...               # {inst: Bar} once per bar time (cross-sectional)
    def on_timer(self, ctx, name): ...
    def on_position_closed(self, ctx, trade): ...
    def status(self, ctx): return "one line for the instance card"
```

Handlers return a Signal, a list or None. Levels take a price, `"<k>atr"` or `"<k>%"`; `sig.reversed()` closes the instance's opposite position and opens in one step (the new side is checked first; exempt from the re-entry cooldown). Param values travel as numbers (bool 0/1, choice/timeframe the option index). `ctx`: `bars series factors screener rank score position(s) account spec state submit log notify`. `ctg.portfolio`: `Watchlist`, `pick`. `ta` mirrors Pine names with numpy only.

Strategy discovery: Go scans `strategies/library/*.py` metadata through the supervisor (`{"type":"describe"}` → list of {file, class, name, description, timeframe, params, universe, meta, code}) at start and on `POST /api/v1/trade/strategies/reload`; each distinct code hash becomes a `StrategyVersion` (built-in, read-only in the editor).

User strategies (`trade_strategy_sources`, `internal/trade/sources.go`): drafts edited in the web editor (autosave + `POST /trade/check`), `POST /trade/sources/{id}/publish` checks and stores an immutable version (`meta.sourceId`); `POST /trade/trial` replays code or a version over the last 1–60 days in a hidden throwaway backtest account (≤ 90 s) and returns signals, trades, run log, report and candles. Writes that run code need the owner key (`internal/api/admin.go`).

## 3. Order layer rules

Pipeline per signal: validate (TTL, instance running, no pending signal for the same instrument, spec exists) → exec mode (record → `recorded`; ai → `Reviewer.Review` with ctx deadline = TTL; unavailable → account `ConfirmDefault`-like fallback: skip) → human confirm if `Limits.Confirm` matches (Interactor.Ask; AI rationale included) → risk (max positions, leverage cap, max instrument margin, daily loss breaker, cooldown, require stop) → sizing (`qty = equity × RiskPct × strength × sizeScale / (|entry − stop| × ctVal)`, capped by leverage/margin/MaxInstPct, rounded down to lot, ≥ minSz else reject) → order group (entry market/IOC + stop + target, OCO, reduce-only) → SimBroker.

SimBroker:
- Live: market/IOC walks `Quote.Asks/Bids` up to the worst price `mid × (1 ± bp/1e4)`; rest cancelled. No depth → `Last ± max(halfSpread, 1 tick)` plus 1 tick. Limit fills when price trades through. Stops/targets are checked on every price tick (≤ 2 s polling of `Quote` for instruments with open orders) and fill as market without the slippage cap.
- Backtest: market fills at next bar open ± half spread (from the 1m spread factor if known, else 2 bp) + 1 tick; within one bar, stop has priority over target; triggers use bar high/low.
- Fees taker/maker from limits; funding charged at settlement times from recorded funding samples (`FundingRate`); isolated margin = notional / leverage; maintenance margin rate 0.5% flat in v1 (TODO tiers); liquidation when mark crosses liq price → whole position closed at liq price, margin lost, `liquidation` ledger entry.

Daily breaker: day boundary UTC+8; when tripped, opening signals are rejected until the next day and an `breaker` event is sent.

Implementation notes (`internal/trade`):
- Timeframes are aligned to UTC epoch multiples (1D = 00:00–24:00 UTC, i.e. OKX `1Dutc`; 4H matches OKX). Live bars are aggregated from recorded 1m (1H+ from stored 1H plus 1m for recent hours) and delivered when the pool clock confirms their last minute (`OnMinute`, registered with `Engine.OnConfirmed`).
- Mark price = last trade price (v1). Live stops/targets/limits/liquidations are polled every 1.5 s from the in-memory quote; executions (market entries, closes, triggered stops) fetch a 50-level book.
- Leverage defaults to `Limits.MaxLeverage` (capped by the contract) and is lowered so the liquidation price stays at least 25% of the stop distance beyond the stop. Without a stop (`RequireStop` off), margin = equity × MaxInstPct × strength × scale.
- Positions are per (instance, instrument); opposite positions in one instrument are rejected; adding re-creates the OCO stop/target for the whole position. AI review applies to opening signals only.
- Backtests: base resolution 1m when any instance runs below 1H, else 1H; a signal at a bar close fills at the next base bar's open. Confirmation is skipped and `ai` behaves like `auto` (an AI reviewing history it may remember is look-ahead). `Factors` are the replayable fields at the last hour close; `Score`/`Screener` use hourly `score_snapshots` (live record over replay) and per-minute `universe_snapshots`; missing history fails the run with a message. Progress is `Account.backtest` {state, progress, clock, error}.
- Funding is charged for every settlement hour a position was held through, from settled rates (`FundingRate`); live waits up to 10 min for the settled sample.
- A Session error other than `ErrFaulted` is a strategy handler exception: 3 in a row pause a live instance (fail a backtest).

## 4. REST API (all JSON, under `/api/v1`)

Trade (`internal/trade`):
- `GET trade/accounts` · `POST trade/accounts` {name, mode, deposit, limits, from?, to?} · `GET trade/accounts/{id}` (account + positions + open orders) · `PATCH trade/accounts/{id}` {name?, limits?, archived?} · `POST trade/accounts/{id}/deposit` {amount}
- `GET trade/accounts/{id}/equity?from&to` → [[ts, equity]] (1m live / bar backtest, downsampled ≤ 2000 points)
- `GET trade/accounts/{id}/fills|orders|signals|ledger?limit&before`
- `POST trade/accounts/{id}/close` {instId?} (manual close one or all) · `POST trade/backtests/{id}/run` (re-run, 202) · `POST trade/backtests/{id}/cancel`
- `GET trade/accounts/{id}/report` → {gross, net, maxDrawdown, sharpe, winRate, profitFactor, turnover, fees, funding, trades, byInst[], signals{ordered,rejected,skipped}} — net = equity − deposits (incl. unrealized); gross = net + fees − funding; sharpe on daily (UTC+8) equity returns × √365, null under 5 days; trades are round trips (open → flat) per instance and instrument
- `GET trade/strategies` (latest version per name + versions list) · `GET trade/strategies/{versionId}` (with code) · `POST trade/strategies/reload`
- `GET trade/accounts/{id}/instances` · `POST trade/accounts/{id}/instances` {versionId, params, universe, exec} · `PATCH trade/instances/{id}` {params?, universe?, exec?} (new config version) · `POST trade/instances/{id}/{start|pause|resume|stop}`
- SSE: existing `/api/v1/stream` gets event types `trade` (account id + kind) so pages refetch.

Interaction (`internal/interact`): `GET interact/prompts?state=open` · `POST interact/prompts/{id}` {choice} · `GET|PUT interact/config` (telegram token masked, allowlist user ids, enabled kinds) · `POST interact/test`.

AI (`internal/aigate`): `GET ai/status` {configured, kind: api_key|oauth_token, masked, storage, workerOnline, workerReady, proxyOk, proxyError, available, model, lastCallAt, lastError, costToday, callsToday, pendingProposals} · `PUT ai/credential` {kind, value} · `DELETE ai/credential` · `PUT ai/model` {model} · `POST ai/test` · `GET ai/decisions?accountId&instanceId&kind&strategy&limit&before` · `GET ai/decisions/{id}` (with the full context pack) · `GET ai/proposals?state=` · `POST ai/proposals/{id}` {approve: bool} · `GET ai/memory/{strategy}?version=` · `POST ai/memory/{strategy}/reflect`.

## 5. AI worker protocol (NDJSON over Unix socket)

Socket `$CTG_RUN_DIR/ai.sock`. Worker connects, sends `{"type":"hello","worker":"1"}`; Go sends `{"type":"config","credential":{"kind","value"},"model":"..."}` (and again when changed), then `{"type":"review","id":"<uuid>","request":{ReviewRequest},"deadlineMs":...}`; worker answers `{"type":"decision","id","decision":{Decision without id}}` or `{"type":"error","id","error"}`. `{"type":"test","id"}` → a minimal call to verify the credential. Heartbeat `{"type":"ping"}`/`pong` every 15 s.

Additions: the worker's `hello` also carries `sdk`, `cli`, `ready`, `proxyOk`, `proxyError`, and it sends `{"type":"status","ready","proxyOk","proxyError"}` whenever they change (Go treats the AI as available only when the worker is ready and a credential is set). `{"type":"reflect","id","request":{strategy, description, timeframe, currentVersion, current, decisions[]},"deadlineMs"}` → `{"type":"result","id","result":{memory, changes, model, costUsd, latencyMs, promptVersion}}` distils a new experience-document version (daily 00:05 UTC+8 for strategies with new decisions). `test` is answered with `{"type":"result","id","result":{reply, model, costUsd, latencyMs}}`. `decision` messages carry `meta` {promptVersion, promptSha, model, costUsd, latencyMs, usage, numTurns, toolCalls}. Go sends `{"type":"cancel","id"}` when a call's deadline passes.

The worker uses `claude_agent_sdk.query` with no built-in tools (`tools=[]`, no MCP servers, `permission_mode="dontAsk"`, no setting sources, no session persistence), a versioned system prompt (`ai/prompts/review_v1.md`) describing the role and hard limits, and the request rendered as a compact context pack; the model answers through the SDK's JSON-schema structured output (execute/skip, size scale, stop/target, confidence, rationale, optional parameter proposal), which is simpler than in-process MCP tools and needs no tool search. Credential via env `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` set per call. All Anthropic traffic goes through the proxy in `CTG_AI_PROXY` (never direct); without a healthy proxy the worker is not ready and refuses calls. No claude.ai OAuth flow in the UI (Agent SDK terms); the owner pastes an API key or a token from their own `claude setup-token`.

## 6. Deployment (ctg-prod)

`/opt/ctg/docker-compose.yml` adds two services built from `strategies/Dockerfile` and `ai/Dockerfile` (`python:3.12-slim`), sharing a `run` volume for the sockets with `ctg`. `ctg-strategy`: `network_mode: none`, read-only rootfs, `mem_limit: 768m`, strategies dir bind-mounted read-only. `ctg-ai`: `network_mode: host` (Anthropic API only through `CTG_AI_PROXY`, a tunnel on the host loopback), `mem_limit: 512m`. `deploy/deploy.sh` uploads `strategies/` and `ai/` and runs `docker compose build` only when their content hash changed.
