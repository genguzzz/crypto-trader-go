# Strategies

Python strategies for the paper-trading layer, written against the `ctg` SDK (protocol version 2).
A strategy only **emits signals**; the Go order layer (`internal/trade`) validates, risk-checks, sizes
and executes them. No strategy can place an order, read credentials or reach the network.
Protocol and contract: [docs/trading-design.md](../docs/trading-design.md) §2.

**The user guide (Chinese) is the in-app help at `/help/strategy`** (sources in
`web/src/docs/strategy/`): quick start, lifecycle, params, `ctx`, signals and sizing, shorting and
reversing, portfolios, notifications, backtests, troubleshooting, and a walk-through of the built-ins.

```
strategies/
  ctg/            SDK: Strategy, params, Screener, Signal, ctx, ta, Watchlist/pick, check
  library/*.py    built-in strategies (read-only in the editor; Chinese comments by design)
  runner.py       supervisor: lists strategies, runs one process per instance
  instance.py     entry point of one instance process
  tests/          unittest suite
  Dockerfile      python:3.12-slim + numpy, uid 65532, read-only friendly
```

## Where strategies come from

- **User strategies** are written in the web editor: a mutable draft, checked by `ctg/check.py`,
  trial-run over the last N days (throwaway backtest account, 90 s budget), then published as an
  immutable version. Built-ins are read-only; the editor's "复制为新策略" (copy as new strategy) starts a draft from one.
- **Built-ins** live in `library/` (one class per file). After editing, rescan with
  `POST /api/v1/trade/strategies/reload`.

Every distinct source becomes a version (id = first 12 hex of sha256(code)); instances keep the
version they were created with.

## Minimal example

```python
from ctg import Float, Int, Signal, Strategy, ta


class MyBreakout(Strategy):
    """BTC 1H breakout."""

    name = "my-breakout"                      # 2-40 of [a-z0-9-]; default: kebab-case class name
    universe = ["BTC-USDT-SWAP"]              # or Screener(rule="resid-reversal", side="both", top=10)
    timeframe = "1H"                          # 1m 5m 15m 1H 4H 1D, or Timeframe("4H") per instance
    can_short = False

    lookback = Int(20, min=5, max=100, label="Lookback bars")
    stop_atr = Float(2.0, min=0.5, max=6, step=0.25, label="Stop (ATR)")

    def on_bar(self, ctx, bar):
        if ctx.position(bar.inst) is not None:
            return None
        b = ctx.bars(bar.inst, n=self.lookback + 40)
        if len(b) < self.lookback + 2:
            return None
        if bar.c > ta.highest(b.h, self.lookback)[-2]:
            return Signal.open_long(bar.inst, stop_loss=f"{self.stop_atr}atr",
                                    take_profit=f"{2 * self.stop_atr}atr", reason="breakout")

    def status(self, ctx):
        return "waiting for a breakout"
```

## API summary

**Class attributes**: `name`, `description` (default: docstring), `universe` (list of up to 50
instIds or `Screener(rule, side=long|short|both, top, min_score=0..100, filter, refresh=5m..1D, scope="pool")`;
`scope="pool"` ranks the active pool by the rule's score (top 1..50), `scope="market"` ranks every recorded
crypto USDT swap by 12h USDT turnover (top 1..300, no rule / min_score)),
`timeframe`, `can_long` / `can_short` (declared directions; the order layer rejects opens of an
undeclared side unless both are False),
`warmup` (informational), `schedule` (`{"name": "15m"}` → `on_timer`).

**Params**: `Int`, `Float`, `Bool`, `Choice(default, options, labels=None)`, `Timeframe(default, options)`
and the SDK 1 `Param`; all take `group`, `label`, `desc`, `ai_tunable`. Every value travels as a
number (bool 0/1, choice/timeframe the option index) and reads back typed as `self.<name>`.
`self.allows(side)` combines `can_long`/`can_short` with an optional `direction` Choice.

**Handlers** (all optional; return a `Signal`, a list, or `None`):

| handler | called |
|---|---|
| `on_start(ctx)` | after (re)start; `ctx.state` restored live, empty in backtests |
| `on_universe_change(ctx, added, removed)` | universe changed; also once after every start |
| `on_bar(ctx, bar)` | per instrument per confirmed bar (bars close on UTC-aligned boundaries) |
| `on_bars(ctx, bars)` | whole bar time at once (`{inst: Bar}`); overriding it replaces `on_bar` |
| `on_timer(ctx, name)` | a `schedule` timer is due |
| `on_fill(ctx, fill)` / `on_position_closed(ctx, trade)` | fills of this instance; the latter when `fill.closed` (position now flat, also the close half of a reverse) |
| `on_signal_result(ctx, result)` | `outcome`: pending ordered rejected skipped_ai skipped_user expired recorded |
| `on_funding(ctx, inst, rate, amount)` / `on_stop(ctx)` | funding settled / before exit |
| `status(ctx)` | after every event; one line (≤ 300 chars) on the instance card |

There is deliberately no per-second callback: history is 1m/1H only, so it could not be
backtested; stops, targets, trailing stops and time limits run in the Go order layer on live quotes.

**Signals**: `Signal.open(inst, side, ...)`, `open_long`, `open_short`, `close(inst)`,
`reduce(inst, frac)`; `.reversed()` closes an opposite position and opens in one step (exempt from
the re-entry cooldown; the new side is checked before anything is closed: `反手被拒：…`). Levels (`stop_loss`, `take_profit`, `trailing_stop`) take a price,
`"<k>atr"` (ATR(14) of the instance timeframe) or `"<k>%"`; `time_limit` takes `"30m"`/`"6h"`/`"2d"`.
Also `strength` (0..1], `leverage`, `ttl_ms` (default 5 min), `max_slippage_bp`, `reason`.
Sizing: `qty = equity × riskPct × strength × scale / (|entry − stop| × ctVal)`, capped by leverage,
per-instrument margin and lot size (`internal/trade/risk.go`). One pending signal per instrument.

**ctx**: `bars(inst, tf=None, n=200)` (confirmed only, n ≤ 1000; backtests build finer timeframes from 1m history), `series(inst, field, tf, n)`
(`oi`, `funding`, `buy`, `sell`, `delta`), `factors(inst)`, `rank(rule, side, among, min_score, top)`,
`screener(rule, side, top, min_score, filter, scope)`, `score(rule, inst)` (NaN for an unscored side), `position(inst)`, `positions()`, `account()`, `spec(inst)`,
`state` (JSON dict ≤ 64 KB, persisted after every event), `submit(signal)`, `log(msg, level)`,
`notify(text, level, **data)` (live accounts only; ≤ 1000 characters, 30/hour, identical text once per 10 min),
`now`, `mode`, `params`, `universe`, `timeframe`, `instance_id`.

**ta**: Pine-like, numpy only, NaN-safe: `sma ema rma rsi atr tr macd bbands crossover crossunder
cross highest lowest stdev change`. **portfolio**: `Watchlist` (candidates in `ctx.state`) and `pick`.

## Limits

Each event must be acknowledged within **5 s** (plus 0.5 s per instrument in a bar batch) or the
process is killed and the instance faulted (restarted with its saved state up to 3 times, then
stopped; a fault after 10 minutes of healthy running counts as the first again).
A handler exception is logged and acknowledged; three in a row pause a live instance and fail a
backtest. Each process runs with a 256 MB address-space limit (`CTG_MEM_LIMIT_MB`), a CPU-time limit
(`CTG_CPU_LIMIT_S`, default 6 h), one BLAS thread and, in Docker, no network and a read-only
filesystem. Only `numpy` and the standard library are available.

## Running locally

```sh
python3 strategies/runner.py                       # connects to data/run/strategy.sock (CTG_RUN_DIR / CTG_SOCKET)
cd strategies && python3 -m unittest discover -s tests
go test -tags pyintegration ./internal/strategyhost/  # Go host + real runner.py round trip
```
