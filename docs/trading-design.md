# Trading design (strategies, paper trading, interaction, AI)

Implements [#3](https://github.com/genguzzz/crypto-trader-go/issues/3). Owner-approved proposal (Chinese): Claude Docs artifact "crypto-trader-go 策略与模拟盘重构方案" (2026-10-02). Shared Go types: `internal/trade/contract.go`.

Scope of this release: strategy SDK + runtime, order layer with a simulated broker, live paper accounts and backtest accounts, a two-way interaction layer (Telegram first), an optional AI review layer, and the web UI. **No real orders, no OKX account access**: `ModeReal` is rejected. Real orders exist only as AI trade proposals the owner approves (§7, approved 2026-11-01); paper trading is unaffected.

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

Universes: a static list (≤ 50 instIds) or a `ScreenerQuery` {scope, rule, side, top, minScore, filter, refresh} re-evaluated every `refresh`. Scope `""`/`pool` ranks the active pool by the rule's score on `side` (else by pool rank), top 1–50. Scope `market` ranks every recorded (eligible) crypto USDT swap (instCategory 1; stocks, commodities and indices excluded) by 12h USDT turnover, descending, `rank` = position, top 1–300; a rule or minScore is rejected (scores exist only for pool rows), `filter` still applies (replay rows carry fact-panel `values` only when a filter is set; live rows always do — do not read `values` from a market screener in a strategy meant to be backtested; a replay `filter` sees only the replayable fact-panel fields, so live-only fields such as `spread_bps` or `basis` match nothing there). A gap in the stored 1H history (fewer than half of the last ranking left) keeps the last ranking for up to a day. Live uses the confirmed snapshot's `Turnover12h` (full 720-minute window, or the provisional value while warming; new and stale instruments are left out). Backtests sum `candles_1h.quote` over the 12 complete hours before the clock (one primary-key range query per crypto instrument, cached per hourly window; `last` = the close of the last complete hour), leaving out instruments missing any hour and instruments without a current spec (delisted: slight survivorship bias); no data at all fails the run. The pool definition is unchanged. Large universes: a bar batch gets 5 s + 0.5 s per instrument (≈ 155 s at 300); live `Keep` is batched into one stream-subscription update; sub-1H backtests load 1m history per universe instrument (memory grows with universe × range).

Timeouts: an event not acknowledged within 5 s → kill the process, instance `faulted`; supervisor restarts it up to 3 times (state restored), then `stopped`. Python exceptions inside a handler are logged and the event is acknowledged; 3 consecutive → `paused`.

The supervisor (`strategies/runner.py`) polls `GET` over the same socket: `{"type":"supervise"}` connection receives `{"type":"spawn","instance":12,"token":"...","file":"library/majors_trend.py","class":"MajorsTrend"}` and `{"type":"kill","instance":12}` commands, and reports `{"type":"exited","instance":12,"code":1,"stderr":"tail"}`. `{"type":"check","id","code"}` runs `ctg.check` on editor code in a throwaway process → `{"type":"check_result","id","result":{diagnostics, strategies}}`. `{"type":"cond","id","req"}` → `{"type":"cond_result","id","reply"}` runs a script-signal request in the script worker (§8, script signals); the supervisor connection accepts lines up to 16 MB for it. One subprocess per instance (memory limit via `resource.setrlimit`, 256 MB).

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

Steering (2026-11-01): `{"type":"steer","id","callId","message"}` hands a further user message to the running chat call `callId`; the worker answers `{"type":"result","id","result":{"ok":true}}` when it was injected (the agent answers it in the same call) or `{"type":"error","id"}` when that turn is not running or already produced its result (Go then keeps it queued and starts a new turn after the current one). A chat turn runs the SDK in streaming-input mode: the question, then every steer, until the first result closes the inbox.

Additions: the worker's `hello` also carries `sdk`, `cli`, `ready`, `proxyOk`, `proxyError`, and it sends `{"type":"status","ready","proxyOk","proxyError"}` whenever they change (Go treats the AI as available only when the worker is ready and a credential is set). `{"type":"reflect","id","request":{strategy, description, timeframe, currentVersion, current, decisions[]},"deadlineMs"}` → `{"type":"result","id","result":{memory, changes, model, costUsd, latencyMs, promptVersion}}` distils a new experience-document version (daily 00:05 UTC+8 for strategies with new decisions). `test` is answered with `{"type":"result","id","result":{reply, model, costUsd, latencyMs}}`. `decision` messages carry `meta` {promptVersion, promptSha, model, costUsd, latencyMs, usage, numTurns, toolCalls}. Go sends `{"type":"cancel","id"}` when a call's deadline passes.

The worker uses `claude_agent_sdk.query` with no built-in tools (`tools=[]`, no MCP servers, `permission_mode="dontAsk"`, no setting sources, no session persistence), a versioned system prompt (`ai/prompts/review_v1.md`) describing the role and hard limits, and the request rendered as a compact context pack; the model answers through the SDK's JSON-schema structured output (execute/skip, size scale, stop/target, confidence, rationale, optional parameter proposal), which is simpler than in-process MCP tools and needs no tool search. Credential via env `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` set per call. All Anthropic traffic goes through the proxy in `CTG_AI_PROXY` (never direct); without a healthy proxy the worker is not ready and refuses calls. No claude.ai OAuth flow in the UI (Agent SDK terms); the owner pastes an API key or a token from their own `claude setup-token`.

## 6. Deployment (ctg-prod)

`/opt/ctg/docker-compose.yml` adds two services built from `strategies/Dockerfile` and `ai/Dockerfile` (`python:3.12-slim`), sharing a `run` volume for the sockets with `ctg`. `ctg-strategy`: `network_mode: none`, read-only rootfs, `mem_limit: 768m`, strategies dir bind-mounted read-only. `ctg-ai`: `network_mode: host` (Anthropic API only through `CTG_AI_PROXY`, a tunnel on the host loopback), `mem_limit: 512m`. `deploy/deploy.sh` uploads `strategies/` and `ai/` and runs `docker compose build` only when their content hash changed.

## 7. Real trades via approved proposals (2026-11-01)

The owner approved real orders on his OKX account under one rule: the agent proposes, the owner approves every action, and only the shapes below exist. Dry run until he sets `CTG_TRADE_MODE=live`. First approval (2026-11-01): market opens, isolated (the default margin mode of an open is now cross, the owner's preference; isolated only on request), TP + SL attached. Extended the same day: cross margin, limit orders, adds (补仓), partial and full closes (减仓 / 平仓) and TP/SL adjustments — each one a proposal he approves — and the agent manages each position over its life, told after every execution.

| Piece | Owns |
|---|---|
| `internal/okxorder` | the only order code. `Build` (open), `BuildAdd`, `BuildReduce` (reduce / close), `BuildAdjust`, `CancelOrder`, `CancelAlgos` are the only constructors of an `Order` (unexported fields, validated). The dry-run client has no fields and no HTTP client; the live client signs through `okxacct.Config` (same key, same `OKX_PRIVATE_PROXY` transport) and sends only the allow-listed paths with allow-listed body keys (below). No transfer, withdrawal, account-setting, batch or amend-order code exists |
| `internal/realtrade` | proposals (`trade_proposals` + `trade_proposal_events` audit trail), approval, re-validation, execution, resting limit orders, position bookkeeping and close detection (`follow.go`), the messages to the chat (`steer.go`, `trade_steers`), triggers (`trade_trigger_runs`), the Telegram trade bot (`trade_tg_msgs`), REST `/api/v1/realtrade/*` |
| `internal/devapi` | owner-only MCP tools `propose_trade` (creates a pending proposal; returns `proposalId`) and `get_trade_proposals` (`posId` = one position's actions); the agent's MCP header `X-Ctg-Chat` links a proposal to its chat (owner key only) |
| `ai/` | `chat_v2.md` `<real_trades>`: the kinds, the position lifecycle, discipline (no averaging down past the invalidation, the stop only moves closer, reasons for every action), never claim an order exists before the server's ✅ |

### Kinds, order types, margin modes

| kind | requests (in order; the first failing one that is not optional stops the rest) |
|---|---|
| `open` | `POST /api/v5/account/set-leverage` {instId, lever, mgnMode, posSide only for isolated in long/short mode (cross leverage is per instrument)} → `POST /api/v5/trade/order` {tdMode = margin mode, side, posSide, ordType market \| limit (+px), sz, `clOrdId=ctgp<id>`, `attachAlgoOrds` [{`attachAlgoClOrdId=ctga<id>`, tpTriggerPx, slTriggerPx, ordPx −1 (market), trigger type last}]} |
| `add` | TP/SL for the whole position (below) → `POST trade/order` {the position's tdMode and posSide, ordType, sz, `ctgp<id>`}, no leverage call, nothing attached |
| `reduce` | if ctg's TP/SL on the position are size-bound: one whole-position TP/SL at the same levels + cancel the old ones → `POST trade/order` reduce-only {opposite side, posSide (long/short mode) or `reduceOnly: true` (net mode), sz = ⌊position × fraction⌋ lots, ≥ minSz, < all} |
| `close` | market: `POST trade/close-position` {instId, mgnMode, posSide, `autoCxl: false`, `ctgp<id>`} → `POST trade/cancel-algos` for ctg's TP/SL on it (optional); limit: a reduce-only order for the whole size, ctg's TP/SL cancelled once it filled. ctg's resting orders in the coin are cancelled first (an add must never fill after the close) |
| `adjust` | TP/SL for the whole position only |
| `cancel` | ctg's resting limit order in a coin and side: `cancel-order` by client id (a partial fill stays a position; settled by the order tracker) |
| `amend` | that resting order: `amend-order` with a new price and, for an open, new TP/SL on its attached algo (`ctga<id>`); a new rest time (`orderExpiryMin`) is bookkeeping only. Size never changes; a partly filled order takes only a new rest time. Re-checked at approval; fails if the order no longer rests |

TP/SL for the whole position: OKX allows one whole-position TP/SL (`closeFraction: "1"`) per position, which closes whatever size the position has when it triggers — so it stays right through adds, partial closes and partial fills. If the position has one (ctg's, or the owner's — then the proposal names its algoId, which the owner approves), `POST trade/amend-algos` {algoId, newTp/SlTriggerPx, ordPx −1, trigger type last}; otherwise `POST trade/order-algo` {ordType oco, `closeFraction: "1"`, opposite side, posSide / `reduceOnly` (net mode), tp/sl, `algoClOrdId=ctgt<id>`}. Then ctg's own size-bound TP/SL (client id `ctga…`) are cancelled (optional: a failure is a warning, the new one already covers the position). The owner's own size-bound TP/SL are never touched; the proposal says they stay. This step runs before the add / reduce order, so size is never added without the stop on it; if it fails nothing else is sent.

Order types: `market`, or `limit` with `price`. A new limit must rest (a buy at or below the price, a sell at or above; `CTG_TRADE_MAX_LIMIT_PCT` from it) — a marketable limit is refused as a market order in disguise; at approval the price may have crossed it, and the order then fills at once at a better price. Exits are checked against the limit price as the entry and against the current price (neither may trigger at once). It rests `orderExpiryMin` (5 – `CTG_TRADE_LIMIT_MAX_MIN`, ≤ 1440, default 60); then the server cancels the unfilled rest by its client id (`POST trade/cancel-order` {instId, clOrdId `ctgp<id>`}).

Margin modes: `isolated` (default) or `cross` for an open; other kinds use the position's. Sizing is the same for both: contracts = margin × leverage / (entry × ctVal), rounded down to lotSz. Liquidation check (the stop within 80% of the distance): isolated ≈ 1/leverage − 0.5% from the entry; cross ≈ available equity / notional − 0.5% (the account's `availEq` from the snapshot, for an add the whole position's notional); without the equity (dry run, no account) the check is skipped and the proposal says so (`note`). Caps (leverage range, margin range per open / add, stop, target, reward:risk, max open, daily loss, drift) apply to both.

The live client's allow-list (`live.go`): set-leverage, trade/order, trade/close-position, trade/cancel-order, trade/order-algo, trade/amend-algos, trade/cancel-algos — each with its own allowed body keys; `instId` must be a `-USDT-SWAP`; `tdMode` / `mgnMode` isolated or cross; orders and closes carry a `ctgp` client id, placed TP/SL a `ctgt` id and `closeFraction: "1"`, cancel-order only a `ctgp` client id, cancel-algos 1–10 algo ids. All requests of an action are checked before the first is sent.

### Lifecycle

Proposal: `pending → approved → executing → executed (dryrun | live)`; `executing → failed` (OKX or network error, requests and responses kept); `approved/executing → invalidated` (re-validation failed, nothing sent); `pending → rejected | expired`. Every transition is a conditional `UPDATE … WHERE status IN (…)`, so the first answer from any channel wins and later ones get 409 / "已结束" with the final state. Expiry (default 15 min, 3–240) never approves. A restart invalidates `approved` and fails `executing` proposals (never retried: the owner checks OKX). Proposals made before a mode switch are void.

Executed limit orders: `ordState live → partially_filled → filled | canceled` (followed every 20 s: live from `GET trade/orders-pending`, then `orders-history`, by client id; a dry run fills when the last price reaches the limit). A market order is `filled` at once. An open that never filled frees its slot (`closeKind unfilled`).

AI positions: an executed open is one (`posId` = its id); later proposals on the same instrument and side link to it. ctg books what it knows — current size, average, TP/SL (`curContracts`, `curAvgPx`, `curSl`, `curTp`) — on every fill; the dry run follows those (the last price crossing SL or TP closes it, P&L estimated before fees, partial closes included); live, the position gone from the account snapshot closes it with P&L and close price from `/api/v5/account/positions-history`. A closed position cancels ctg's orders still resting on it and its linked signals (§8). Actions on the owner's own positions (no AI open) are allowed for add / reduce / close / adjust (`posId` 0, not booked).

Checks at creation and again at approval: a fresh last price (≤ 90 s); for market opens and adds the drift since the proposal ≤ `maxDriftPct`; the order rules above (re-built against the live price and a fresh position snapshot: still there, same side, same AI position; an add's margin mode and leverage unchanged; a reduce re-sized from the current size); one proposal in flight per instrument; an open only where there is no AI position, no resting ctg order and no position of the owner's (he gets add / reduce / close / adjust instead), at most `maxOpen` AI positions; add / reduce / adjust not while a ctg limit order rests in the coin (a close may: it cancels them); at most `CTG_TRADE_MAX_ADDS` adds per position; the daily loss stop (today's UTC+8 realised P&L of AI positions ≤ −`dailyLossUsdt`) blocks opens and adds only — reducing risk is always allowed; at creation at most `maxPending` waiting proposals. Position mode from `GET /api/v5/account/config` (cached 10 min; a dry run without an account assumes long/short mode).

### Telling the chat (steer)

After an approved proposal ran — executed (a market fill, a limit placed, TP/SL moved), a limit filled or cancelled (partly filled or not), failed, invalidated — and when its AI position closed, the chat that proposed it (for signal / schedule runs: that run's chat) gets a `role: "trade"` message: ✅ / 📌 / ⌛ / ❌ / ⚠️ / 📕, the order (fill price, size, margin mode, leverage), what happened to the TP/SL, the position now (OKX snapshot, or the dry-run simulation: size, average, liquidation, mark, unrealised P&L, each TP/SL with whether ctg placed it and whether it covers the whole position), every action on the position so far, its signals, and what to do (verify with `get_positions`, leave the next signals with `tradeId`, propose adjustments; a closed position: a short review). Live market executions wait 3 s first so the fill shows in the snapshot. It is steered (`aigate.SteerChat`): an idle chat starts a turn with it; a chat that is answering gets it injected into the running turn (§5 `steer`), so it is never held until the turn ends. Only when that fails (the turn is finishing, the worker is down) it stays queued in `trade_steers`, retried when any turn ends and every 5 s, failed after 2 h; one chat's messages keep their order.

Sources: `chat` (the owner's conversation), `signal` (an event trigger) and `schedule` (a time trigger). A trigger (`TriggerDef`: id, kind, label, enabled, `everyMin`, `ruleIds`, `cooldownMin`, prompt template with `{{time}}`, `{{instId}}`, `{{rule}}`, `{{message}}`, `{{value}}`) starts a headless agent run through `aigate.StartChat` titled "信号 · <label>", so it is an ordinary chat with all its steps in the history; a `propose_trade` call from it carries the trigger as source. Built in, disabled by default: `scan` (every 240 min) and `alerts` (any alert rule, or `ruleIds`; fed by `alerts.SetOnFired` → `Service.Publish`). Each trigger has a cooldown (default 60 min) and all together at most `CTG_TRADE_MAX_RUNS_DAY` runs per 24 h (default 12). A new trigger type is one `Kind` (`Due` for time, `Match` for events) plus a default entry.

Approval channels (all update on every change; SSE event `realtrade`):
- Telegram trade bot (`CTG_TRADE_TG_TOKEN`, `CTG_TRADE_TG_CHAT` = the owner's user id; separate from the paper-trading bot): one card per proposal (kind, coin, side, leverage and margin mode, margin and contracts, entry as 市价 / 限价 with its rest time, for an add the position before and after with the new average, for a reduce / close how much of which position, SL/TP with % and reward:risk, the liquidation estimate, what happens to the TP/SL, rationale, invalidation, source, expiry, `[DRY RUN]`/`[实盘]`; once executed the limit order's state), buttons ✅ 同意 (asks ✅ 确认同意 with the exact action / ↩️ 返回 first) and ❌ 拒绝 plus a 🔍 查看 link to the K-line with the levels. The card is edited as the proposal and its order move on and every event (approved, rejected, expired, invalidated, executed, filled, partial, canceled, failed, closed) is also pushed as a short message. Only callbacks from that user id count. Unset = disabled.
- Web and app: a proposal card under the `propose_trade` step in the chat (kind, order type, margin mode, the position it acts on, the order's state; also in signal chats), the trade message in the chat, the pending list with a badge on the AI orb, and the K-line with entry, SL and TP drawn (`?proposal=<id>`; the app's embed chart takes the same parameter). Approving needs an explicit confirm step that states the exact action.
- REST (owner key required for every method): `GET realtrade/proposals?status=pending|open|all&chatId&limit` (open includes resting limits) · `GET realtrade/proposals/{id}` (with `events`) · `POST realtrade/proposals/{id}/approve` {confirm: true, via} → final state, 409 {error, proposal} when no longer pending · `POST realtrade/proposals/{id}/reject` {via} · `GET realtrade/config` (mode, limits, bot status, triggers, account) · `PUT realtrade/triggers`. No API, agent or UI path changes the mode or the caps.

## 8. Signal subscriptions: the agent waits instead of being asked again (2026-11-01)

Owner request: when the agent cannot open a trade yet (entry not reached, the turn not confirmed, P_tail too high), it subscribes to the condition; the signal re-activates the same chat session with a prompt that carries everything needed to decide. `internal/signals`, MCP tools in `internal/devapi`, wake-up in `internal/aigate`.

```
condition (Spec) ──event──▶ subscription fires ──▶ ⚡ 信号 message in the subscriber's chat ──▶ agent re-checks (full history)
  price / zone      (tick 2 s,      edge false→true,       injected into a running turn,        ├─ propose_trade ──▶ owner approves (§7) ──▶ execution
  field / moverisk   minute,        cooldown, maxFires,    Telegram note "AI 正在评估"           ├─ subscribe_signal (adjusted)
  alert / time /     alert)         expiry, caps                                                └─ explain + unsubscribe_signal
  plan / all / any
```

| Piece | Owns |
|---|---|
| `internal/signals` | `Spec` (validation, Chinese description, chart levels, distance to firing), evaluation (`eval.go`), `signal_subs` + `signal_fires`, the event loop, firing, the wake-up queue, caps, MCP handlers, REST `/api/v1/signals*`, the `/go/chat/{id}` bounce |
| `internal/aigate` | `SteerChat` (a `signal`-role message: a new turn in the chat's session, or injected into the running turn; `ErrChatBusy` only when that turn can no longer take it), `SetOnChatDone` (queued wake-ups go when a turn ends), `SetOnPlan` (a plan written in the same answer is linked to that chat's new subscriptions on the coin), `PlanByID` |
| `internal/market` | `KeepFor("signals", ids)` keeps the subscribed instruments streaming; `FieldValues` (pool row or on-demand row) |
| `internal/devapi` | owner-only MCP `subscribe_signal` (spec is a JSON object), `list_signals` (scope mine / all), `unsubscribe_signal` (own chat only unless `ownerAsked`) |
| `internal/realtrade` | a proposal made in a chat a signal woke within 15 min gets source `signal` and label `#id <condition>`; `Bot.Note` pushes fires; `WakeContext` gives a wake-up the state of the position it manages; a closed AI position cancels its linked subscriptions (`CancelForTrade`) |
| `strategies/ctg/condition.py`, `condworker.py`, `runner.py` | script conditions: the `Condition` SDK class, static checks, the per-call CPU budget, the replay loop; the long-lived script worker; the supervisor's relay with crash isolation |
| `ai/` | `chat_v2.md` `<signals>`; step labels 订阅信号 / 取消订阅 / 查看信号 / 测试信号脚本 |

Conditions (leaves; `all` / `any` compose at most 6, two levels; `script` below): `price` (last price above/below a level, or a confirmed `1m/5m/15m/1H/4H` close), `zone` (last price inside lo–hi, or a closed 1m bar that traded into it), `field` (any `features.Fields` key except `turnover12h`, read each confirmed minute), `moverisk` (`pTail` / `pBig3` vs a probability, or `extended` entering / leaving, on confirmed 1H bars), `alert` (a rule fires, optionally for one instrument), `time` (`at` UTC+8 or `afterMin`), `plan` (an `ai_plans` row reaches active = entry touched / tp1 / done / stopped / invalid / missed / expired / settled). Strict validation: unknown keys, instruments, fields, rules or plans, levels > 50% from the price, zones wider than 20%, past times are rejected. Values are tri-state; missing data is unknown and never makes an edge.

Semantics: a subscription fires on the edge from false to true. Subscribing to a condition that already holds is rejected (act now or pick the next level); its last state is persisted, so a condition that turned true while the server was down fires once after the restart. Each event re-reads only the leaves it can change: the engine's price broadcast (2 s) the last-price leaves, each confirmed minute the fields, bar closes, plans, zone wicks and (on the hour) move risk, each alert the alert leaves. `maxFires` (1–5, default 1) with `cooldownMin` (5–1440, default 60); expiry is required (15 min – 7 days, default 24 h); `active → fired | expired | cancelled` with who and why.

Firing appends a `role=signal` message to the subscriber's chat — "⚡ 信号 #id 触发：<condition>", the time and price, the matched values, the subscriber's intent, the linked plan's levels and invalidation, fires left, then the three allowed outcomes and the unattended-session footer — and runs the agent in that chat (same session, full history); a chat that is answering gets it injected into the running turn. A subscription may manage an AI position (`tradeId`, the open proposal's id): its wake-up carries the position block (size, average, liquidation, TP/SL on OKX, every action on it, its signals), a chat subscription without one gets the chat's AI positions in its instruments, and it is cancelled (by system, with the reason) when the position closes. Owner-created subscriptions start a new "信号 · <condition>" chat instead. Only when the injection fails (the turn is finishing, the agent is down) the wake-up is queued (`signal_fires.wake = queued`), retried when any turn ends and every minute; queued over 2 h → failed. Wake-ups per 24 h are capped (`CTG_SIGNAL_MAX_WAKES_DAY`); over the cap the fire is recorded and pushed but nobody is woken. Caps on active subscriptions: `CTG_SIGNAL_MAX_ACTIVE` in total, 5 per chat. Every change is the SSE event `signals` {kind, id, chatId}; the app follows a chat it is showing when a signal wakes it.

REST (owner key): `GET signals?status=active|fired|ended|all&instId&chatId&kind&limit` → {signals (active ones with `progress`: price, target, distance %), caps, now} · `GET signals/{id}` (with `history`: each fire's time, price, matched values, wake state, woken chat, error) · `POST signals` {spec, code, intent, expiresHours, maxFires, cooldownMin, planId, every} (the owner's own) · `POST signals/{id}/cancel` {reason} · `POST signals/test-script` {code} → {ok, problem, reason, diagnostics, meta, replay, limits} (stores nothing; 503 when the runner is down) · `GET signals/script-template`. The embed chart draws a subscription's levels with `?signal=<id>`.

Each subscription has a check interval `every` (tick ≈ 2 s price broadcast, 1m, 5m, 15m, 1H on that confirmed minute; default tick for last-price levels / zones / times, else 1m): tick events skip anything not on tick, minute events skip subscriptions whose interval does not end at that minute. Coarser is cheaper and calmer; a spike between checks can be missed.

### Script signals (2026-11-01)

Owner request: signals as arbitrary programs in the strategy SDK's style, not only the built-in types. Built-ins stay first; a script is for indicator logic, several conditions over bars, or relative moves.

Format — a `ctg.Condition` subclass (the SDK's `Signal` is already the trade signal, hence the name):

```python
from ctg import Condition, Fire, ta

class AaveRsiReclaim(Condition):
    """AAVE 15m RSI 从 30 以下收回"""     # description (≤ 120 chars), shown with the signal
    instruments = ["AAVE-USDT-SWAP"]      # 1–10 instIds
    timeframe = "15m"                     # 1m 5m 15m 1H 4H (no tick)
    lookback = 100                        # bars per instrument, 10–500 (default 200)
    def check(self, ctx, bars):           # {inst: Bars}, newest bar = the one just closed
        r = ta.rsi(bars["AAVE-USDT-SWAP"].c, 14)
        return Fire(r[-2] < 30 <= r[-1], values={"rsi": r[-1]}, note=f"RSI {r[-1]:.1f}")
```

`check` returns True / False, `Fire(hit, values ≤ 12 numbers, note ≤ 200 chars)` or None (unknown). `ctx`: `now` (the close, ms), `timeframe`, `instruments`, `bars(inst, n)`, `factors(inst)` (fact-panel values of the last confirmed minute; none in replays). It is a pure function of the bars: no state survives between calls, so the replay sees what the live checks will. Spec `{"type":"script","code":"…"}` (the tools and `POST signals` also take the code as a separate `code` field); validation stores the declarations in `spec.script`. One script per subscription; it may sit inside `all` / `any`.

Sandbox: scripts run in `ctg-strategy` (no network, read-only root, 256 MB address space, one BLAS thread) in one long-lived worker process (`ctg.condworker`) that the supervisor starts on first use. Guard rails on top: imports only `ctg numpy math statistics collections itertools functools typing` (also enforced at run time by the `__import__` the script gets), no `open eval exec compile getattr …`, no dunder attributes, no numpy file I/O, no bare `except` (the CPU-budget exception is a `BaseException`), no `global`; pyflakes undefined names fail; ≤ 8000 bytes. Each call has a CPU budget (`CTG_SIGNAL_SCRIPT_MS`, SIGPROF, re-armed every 50 ms); loading has 2 s.

Validation (subscribing, `POST signals/test-script`, MCP `test_signal_script`): compile and declarations in the worker; the instruments must exist; then a replay over the last 7 days of the timeframe, as far as 1000 bars (lookback included) reach (with lookback 200: 1m ≈ 13 h, 5m ≈ 2.8 days, 15m and slower 7 days), each close seeing only the bars closed by then. An instrument whose newest bar is not the one closing then (gap, no data) makes that close unknown without running the script — the same rule live. Fires = false→true changes, with unknown keeping the last known value (as live). Rejected (`problem`): `compile`, `error` (raised on any close), `slow` (any close over the CPU budget, or an average over a quarter of it), `unknown` (None on every close, or no value on the newest close), `often` (more than `CTG_SIGNAL_SCRIPT_FIRES_DAY` per day), `holds` (true on the newest close — same rule as every type), `nodata` (no or stale bars). The replay summary (`states` one char per close, the last 50 fires with values, counts, average / max ms, the newest close) is stored on the subscription (`signal_subs.script`) and shown in the app; `noFactors` marks scripts that read factors (undercounted in replays). At most 10 active script subscriptions.

Cadence: a script runs when its timeframe's bar closes, at the confirmed minute that closes it, and only on minutes its subscription's `every` is due (`every` is a minimum interval; `tick` is rejected; the default for a subscription with a script is 1m, so the timeframe decides). From that minute until the answer arrives the script leaf is unknown, so the other leaves of a composite cannot fire on the previous bar's script value. Each such minute sends one batch with every due script — code, the last `lookback` bars cut at the close, the instruments' finite fact-panel values — as `{"op":"eval"}` through `strategyhost.Host.Conditions`; the event loop is not blocked (results come back as a `script` event; a batch still running makes the next one skip). After a restart one catch-up batch checks every script at its newest close; a script that turned true while the server was down fires once (the state is persisted like every type).

Worker protocol (stdin / stdout of `ctg.condworker`, one JSON per line, `{"ready":true}` first, every answer ends with `"end": true`): `check {code}` → `{ok, diagnostics, meta}`; `eval {budgetMs, items[{key, code, now, bars {inst: [[ts,o,h,l,c,vol,volq]…]}, factors}]}` → one line per item `{i, key, fire, values, note, ms, error?, missing?}`; `replay {code, bars, start, end, budgetMs, maxTotalS}` → `{states, edges, edgeCount, errors, timeouts, firstError, unknown, avgMs, maxMs, factors, last}`. Compiled classes are cached by code hash (64); every request carries the code, so a restarted worker rebuilds its cache. The supervisor serves these requests one at a time on one thread (the worker dies with the thread that started it); a worker that dies or stays silent for 5 × budget + 2 s is killed and restarted, the item that was running gets the error (`crash`), and the items after it run again in a fresh process (up to 3 times); a worker that cannot start is an outage (`infra`).

Failures are never silent: a script error (exception, budget, crash, hang) counts; `CTG_SIGNAL_SCRIPT_ERRORS` (3) in a row end the subscription (`cancelled`, `ended_by = system`, reason with the last error) and push "信号 #id 已停止" with a link to the chat. Missing bars and a runner that is down are not the script's fault: the leaf stays unknown and `lastError` says why; 30 minutes of outage are pushed once. The list and detail show `script` {class, desc, instIds, timeframe, lookback, hash, code, replay, last, checks, avgMs, errors, lastError, lastErrorAt}; an active script's progress is its newest check ("15m 收盘 11-01 12:15：未成立 · RSI 41.2").

On the way to an autonomous agent, scripts are how the agent turns a setup it reasons about into something the server can watch exactly — the same SDK it would use for a strategy, tested on history before it is trusted, and still only a wake-up: every trade goes through §7.

### Pool scans (2026-11-02)

Owner request: built-in signals over the whole pool ("any coin in the pool moving X% within N minutes", up, down, or a swing), managed in one place instead of one subscription per coin. `{"type":"pool", …}` is a whole subscription (never inside `all` / `any`), `internal/signals/pool.go`:

| key | meaning |
|---|---|
| `scope` | `pool` (default: the active pool; `top` N = ranks ≤ N), `watch` (watchlist), `list` (`instIds`, 1–50); `exclude` removes coins |
| `metric: move` | last price (or the confirmed close) vs the close `windowMin` minutes earlier (1–1440; 1 = minute to minute), `dir` up / down / both, `pct` |
| `metric: range` | (highest high / lowest low − 1) over the window's 1m bars ≥ `pct` (needs ≥ 80% of the bars) |
| `metric: field` | a fact-panel field with `op` / `value` across the scope |

Members are re-read on every check (`market.PoolMembers`; bars from the in-memory 1m ring, `Recent1m`). Each member has its own edge and cooldown (`cooldownMin` per coin); its first value — at subscribe, on joining the scope, and after leaving and rejoining — only primes it, so coins already beyond the threshold never fire until they fall back first (the subscription is not rejected for them). Members that turn true on the same check are one fire. Checked each confirmed minute (`every` 1m, default) or each price tick; `maxFires` defaults to 10 (≤ 50); the daily wake cap still applies. The per-member state is persisted (`signal_subs.pool_state`), so a restart continues it; the restart's catch-up check only primes.

### Deliveries (2026-11-02)

Every fire is one `Delivery` (`internal/signals/delivery.go`, version 1), stored as `signal_fires.payload` and returned in `GET signals/{id}` history: `{v, subId, fireId, ts, type, metric, desc, fireNo, maxFires, final, expiresAt, cooldownMin, hits[{instId, px, value, text}], more, matched, intent, subscriber, chatId, tradeId, planId}`. Pool hits are the coins that fired, largest |value| first, at most 10 (`more` counts the rest); other types have one hit per instrument with its price. The wake-up message and the push are rendered from it; a pool wake-up lists one line per coin and asks the agent to look at the strongest one or two (the scan keeps running). New consumers (a strategy, a webhook) read the same record.

### Replays of built-in conditions (2026-11-02)

`ReplaySpec` (`replay.go`) runs a spec through the live evaluator (`liveSub.step`, the same edge / cooldown / pool code) minute by minute over 1m bars from a `History`; each minute is seen at its close, a tick first (the close standing in for the last price) and then the confirmed-minute event. Replayable: price, zone, time, `all` / `any` of them, pool scans on move / range; fields, move risk, alerts, plans have no history (rejected) and scripts have their own replay. Live, `test_signal` (MCP) / `POST /api/v1/signals/test {spec, hours ≤ 72, cooldownMin, every}` replays over the recorded 1m bars (`market.Bars1m`; pool members = the scope's coins now) and returns fires, fires per day, fires per coin and the last fires. `GET /api/v1/signals/catalog` describes every type (params, cadence, replayable, example) and the delivery version.

Script checks with fewer than `lookback` bars for an instrument are unknown (`warmup`) and the script is not run — live and in replays — so scripts may index the bars they declared without guarding against short windows at the start of history or for new listings.

### How signals are tested

- `testdata/okx-1m-20261030.csv.gz`: real OKX 1m candles of 10 perpetuals over 30 h (ENA and PEPE move ≥ 2% in 15 min often, the majors never), shared by the Go and Python tests.
- Pool scans: replays equal a hand-written brute force on that history for moves (1, 15, 240 min; up / down / both; several cooldowns) and ranges; only the volatile coins fire at 2%; tick and minute checks agree; the live service driven minute by minute fires exactly where the brute force does (times, coins, delivery fields, ordering), hits the daily wake cap after 30, ends at maxFires, survives a restart without re-firing, and primes coins that join the scope.
- Single types: last-price crosses, 15m closes and zone entries on BTC equal brute force; deliveries and the REST / tool surfaces are checked.
- Scripts (`script_worker_test.go`, needs python3 + numpy): six common conditions (SMA cross, breakout, volume spike, relative strength of two coins, RSI reclaim, any-of-three 1m jump) are subscribed through the real worker (`runner.CondWorker` → `ctg.condworker`), checked live minute by minute as production schedules them, and must fire exactly at the false→true edges of the worker's own replay of the same bars; validation outcomes (compile, error, often, holds, forbidden import, lookback longer than the history) are checked with the real worker. `strategies/tests/test_condition_replay.py` requires the replay's state on every close to equal the condition computed straight from the full arrays (no look-ahead), for several timeframes and two-instrument conditions.

### Towards an autonomous trading agent

The loop is: signals → subscriptions → wake-up → decision (the agent, with fresh data) → proposal → approval → execution (§7). Autonomy can grow only at named points, each a separate owner decision:
- **Approval policy** (not implemented, must stay off without the owner's explicit decision): a policy between `Propose` and `Approve` in `internal/realtrade` (e.g. auto-approve dry-run proposals, or live ones under a smaller margin cap with a stricter reward:risk) — it would call the same `Approve` path with `by = policy:<name>` so re-validation and every cap still apply and the audit trail shows it.
- **Wake budget**: the daily wake cap and per-chat caps are the cost and attention limits; a scheduler that subscribes on its own (e.g. the `scan` trigger subscribing to its best setup instead of proposing at once) uses the same tools.
- **Position follow-up** (2026-11-01, implemented): the agent leaves signals with `tradeId` for each position it manages and is told after every execution; closes, reduces, adds and TP/SL moves are proposals the owner approves (§7).
- **Trading desk** (2026-11-11, implemented, §9): the loop above run by the server in one standing chat, with a decision journal. It adds no way to trade: the approval step is unchanged.

## 9. Trading desk: one standing AI chat run by the server (2026-11-11)

Owner request: a fully automatic AI session — a special conversation that keeps running, listed with the others but visibly different, that the owner can also talk to; pool-wide signal strategies deliver to it without burning tokens on every tick; when nothing fires it still runs on a clock (every 2 hours, 10 minutes before the hour, close to OKX's funding settlements); and its trading memory is data in the database, so decisions can be traced back and counted. `internal/autopilot`; the chat kind in `internal/aigate`; the standing subscriber and the `combo` scan in `internal/signals`.

```
strategy scans (pool-wide, edge + per-coin cooldown) ─fire─▶ inbox ─batch 90 s, gap ≥ 20 min, ≤ 10 / 24 h─▶ run ─┐
schedule: LeadMin before every EveryHours-th hour, UTC+8 (23:50, 01:50, … by default) ─────────────────────▶ run ─┤
owner: "run now", or simply writing in the chat ───────────────────────────────────────────────────────────▶      ┘
   brief: what fired · AI positions and resting orders · proposals waiting · the signals the desk's agent itself waits for · recent decisions with their outcomes · reviews due · memory · budget
   agent: fresh data → desk_journal per coin → propose_trade (owner approves, §7) → subscribe_signal → desk_review / desk_remember
   server: stamps each decision with price + fact panel, follows it 1 h / 4 h / 24 h, MFE / MAE, the proposal's fate → statistics
```

| Piece | Owns |
|---|---|
| `internal/autopilot` | the configuration (setting `autopilot.config`), the built-in strategies and their reconciliation into desk subscriptions, the inbox, pacing and the schedule, the brief, runs, the journal with its follow-up, the memory, statistics, REST `/api/v1/autopilot*`, the four `desk_*` tools |
| `internal/aigate` | `ai_chats.kind` (`desk`): `EnsureChat`, listed first, not deletable; `SteerChatWith` (model / effort of the turn it starts); `ChatTail` / `GET ai/chats/{id}?last=N` (the chat never ends, clients load its tail); `LastAnswer`; `ResetSession`; `SetNotifyFilter` (no "answer ready" push per desk run — proposals are pushed by the trade bot) |
| `internal/signals` | subscriber kind `desk` (`SubscribeInput.Desk` = the strategy key): no expiry, no fire limit, outside the caps and the daily wake cap, fires go to `DeskSink` (`signal_fires.wake = desk`) instead of waking a chat or pushing a note; pool metric `combo` |
| `internal/devapi` | owner-only MCP `desk_journal`, `desk_review`, `desk_remember` (desk chat only) and `desk_recall` (any chat) |
| `internal/realtrade` | unchanged. A proposal made in the desk's chat within 15 minutes of a run gets source `signal` and label `交易台 · <run label>`; executions and closes are steered into the chat as for any other chat |
| `ai/` | `chat_v2.md` `<desk>`; tool allow-list and step labels 记录决策 / 复盘决策 / 写入长期记忆 / 翻交易台记录 |

**The chat.** One row in `ai_chats` with kind `desk`, created when the desk is first enabled. A run is a `signal`-role message that starts with `🤖 交易台 #<run>` delivered with `SteerChat`: a new turn, or injected into the turn that is running (the owner was talking to it). The owner's own messages are ordinary turns in the same session. Every `SessionRuns` runs (default 12 ≈ a day) the agent's session is reset before the brief is sent: the stored messages stay, the model's context starts empty, and the brief says so — what must survive is in the journal and the memory, which every brief carries. This bounds the context a never-ending chat would otherwise re-read on every run.

**Strategies** are pool scans (§8) owned by the desk, built from parameters the owner can change (`Strategies` in `config.go`; each has a hint the brief gives the agent when it fires):

| key | fires when a coin of the pool's top N … | default |
|---|---|---|
| `surge` 急涨急跌 | moves ≥ pct % either way within windowMin | top 60, 15 min, 3 %, cooldown 120 min |
| `momo_up` 放量上攻 | rises ≥ pct % in 1 h **and** volume ≥ volX × the 7-day median **and** OI +oiPct % in 1 h | 4 %, 2×, 2 % |
| `flush_down` 放量下杀去杠杆 | falls ≥ pct % in 1 h **and** volume ≥ volX × **and** OI −oiPct % in 1 h | 4 %, 2×, 2 % |
| `oi_build` 持仓激增 | OI +oiPct % in 1 h **and** a 1 h range ≥ rangePct % | 6 %, 2 % |
| `funding_extreme` 资金费极端 | funding per hour ≥ ratePct % or ≤ −ratePct % (two scans) | top 100, 0.03 %/h, cooldown 480 min |

`Custom` (≤ 8) are pool specs the owner writes himself. The new pool metric `combo` is what makes "several of my indicators at once" one signal: `{"type":"pool","metric":"combo","legs":[{"metric":"move",…},{"metric":"field",…}]}` — 2–4 legs of move / range / field that must all be true for the same coin; unknown if any leg is unknown; the first leg's value ranks the hits; replayable when no leg reads a field. `reconcile` keeps the subscriptions equal to the configuration (a hash of spec + cooldown per key in `desk_strategy_subs`): a changed parameter replaces that scan only; a scan that cannot start (no pool data yet, an invalid custom spec) is reported on its strategy and retried each minute.

**Pacing** (the token budget). Scans are edge-triggered with a per-coin cooldown, so a coin is news once. A fire becomes inbox items (`desk_inbox`, one per coin) and never wakes anyone by itself. A signal run starts when the oldest pending item has waited `BatchSec` (90 s: fires of the same minute travel together), the last run started ≥ `MinGapMin` (20) ago, no run is in flight and fewer than `MaxRunsDay` (10) signal runs started in the last 24 h. What does not fit waits: it is folded into the next scheduled run, or dropped as stale after `StaleMin` (120). A run that cannot be delivered (agent down, the turn cannot take it) leaves no row; the slot or the items stay due and are retried every 2 minutes — a schedule slot for 30 minutes, then it is skipped (also after downtime: no burst of late runs). Enabling the desk inside that window runs the current slot at once. A run cut off by a restart is marked failed, its slot becomes due again and the signals it carried go back to the inbox. The owner is not told about ordinary runs (the "answer ready" note is suppressed while a run is in flight; proposals are pushed by the trade bot), but a run that failed is pushed, and so is the first of a series of runs that cannot start (once per outage).

**Schedule.** Slots start `LeadMin` (0–30, default 10) before every multiple of `EveryHours` (1 / 2 / 4 / 8, default 2) on the UTC+8 clock, so with the defaults the desk looks at the market at 23:50, 01:50, … — before each 8 h funding settlement (00 / 08 / 16), each 4 h settlement and each 4 h bar close. A scheduled run takes the pending signals with it and otherwise reviews BTC / ETH, the open positions and the pool. The older `scan` trigger (§7 triggers, a new chat every 4 h) does the same job without memory: leave it off when the desk is on.

**Journal** (`desk_decisions`). `desk_journal` records one decision per coin the agent seriously looked at: action (open / add / reduce / close / adjust = made with a `propose_trade`, whose id it carries; hold / watch / skip = passes), side, conviction, thesis, entry / stop / target, invalidation, tags. The server adds the run and its trigger, the strategies that put the coin in front of the desk, the last price and the fact-panel values and BTC's price at that moment (`snapshot`). `follow` (each minute) fills in what happened: the close 1 h / 4 h / 24 h later and the return in percent **signed by the decision's side** (a short that fell is positive; for a pass the side is the trade that was considered, so a positive return means the pass missed a move), the best and worst excursion within 24 h (`mfe_24h`, `mae_24h` from 1m highs and lows), and the linked proposal's status with the close kind and P&L of the position it belongs to (`pos_id`: every action on one position carries the same result, and statistics count a closed position once). Excursions are measured from the first whole minute after the decision. A second `desk_journal` for the same coin in the same run replaces the first. A decision recorded without a fresh price is never scored. Decisions whose outcome is in (a closed position, a trade after 24 h, a pass after which the coin moved ≥ 5 %) appear under 待复盘 in the next briefs until `desk_review` stores a verdict (right / wrong / mixed / na) and the reason.

**Memory** (`desk_memory`, ≤ 60 active). `desk_remember` keeps short reusable statements: `rule` (how the desk trades; the owner's own are written from the app), `lesson` (from a reviewed decision, linked to it), `regime`, `coin`. Rewritten in place (`replaces`), retired rather than deleted. A brief carries up to 14: rules first, then what is about the coins in play, then the newest. It is separate from the chat agent's notes (`ai/notes`), which are about the owner, not about trading outcomes.

**Statistics** (`GET autopilot/stats?days=`, `desk_recall what=stats`): runs and cost; proposals made / executed / rejected / expired, closed positions with wins and realised P&L; and for decisions with a side the count, the share with a positive 4 h return and the average 1 h / 4 h / 24 h return — overall and by action, conviction, strategy and trigger. Small samples are what they are: the app says so.

REST (owner key): `GET autopilot` → {status, config, strategies (definition, setting, running scans with progress, fires in 24 h, error), options} · `PUT autopilot/config` · `POST autopilot/run` {note} · `GET autopilot/runs` · `GET autopilot/inbox` · `GET autopilot/journal?instId&action&days&runId&reviewed&before&limit` · `GET autopilot/journal/{id}` · `POST autopilot/journal/{id}/review` · `GET|POST autopilot/memory` · `DELETE autopilot/memory/{id}` · `GET autopilot/stats`. Every change is the SSE event `autopilot` {kind: config | run | inbox | journal | memory}.

What the desk cannot do: approve, execute, change a limit, or raise its own budget. `Propose` → the owner's `Approve` is the only path to an order, as in §7; the desk off (the default) means no scans, no runs and no cost.

How it is tested: `internal/autopilot` drives the real service with fakes for the gate, the signal service, the proposals and the market on a controlled clock — slots and the grace period, batching, the gap, the daily cap, folding into the schedule, staleness, retries, session rotation, reconciliation on parameter changes and failures, the journal's validation, follow-up (signed returns, excursions, a decision without a price), reviews, memory, statistics, the tools' chat check, and a restart during a run. `internal/signals` checks `combo` replays against a brute force on the real-history fixture and the desk subscriber (never used up, outside the caps, no wake-up, no push); `internal/aigate` the standing chat (creation, order in the list, tail, no delete, model per run, session reset) and the notify filter.

