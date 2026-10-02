# Strategies

Python strategies for the paper-trading layer. A strategy only **emits signals**; the Go order layer
(`internal/trade`) validates, risk-checks, sizes and simulates them. No strategy can place an order,
read credentials or reach the network. Protocol and contract: [docs/trading-design.md](../docs/trading-design.md) §2.

```
strategies/
  ctg/            SDK: Strategy, Param, Screener, Signal, ta, ctx
  library/*.py    strategies (one or more Strategy subclasses per file)
  runner.py       supervisor: lists strategies, runs one process per instance
  instance.py     entry point of one instance process
  tests/          unittest suite
  Dockerfile      python:3.12-slim + numpy, uid 65532, read-only friendly
```

## Writing a strategy

Drop a file into `library/`, then reload strategies in the UI (`POST /api/v1/trade/strategies/reload`).
Each distinct source becomes an immutable strategy version; instances keep the version they were created with.

```python
from ctg import Param, Signal, Strategy, ta


class MajorsTrend(Strategy):
    name = "majors-trend"                                  # stable id (default: kebab-case class name)
    description = "BTC/ETH 4H EMA cross"
    universe = ["BTC-USDT-SWAP", "ETH-USDT-SWAP"]          # or Screener(rule="resid-reversal", side="long", top=10)
    timeframe = "4H"                                       # 1m 5m 15m 1H 4H 1D

    fast = Param(20, min=5, max=100, desc="Fast EMA", ai_tunable=True)
    slow = Param(50, min=20, max=300, desc="Slow EMA", ai_tunable=True)

    def on_bar(self, ctx, bar):
        b = ctx.bars(bar.inst, self.timeframe, self.slow * 3)
        f, s = ta.ema(b.c, self.fast), ta.ema(b.c, self.slow)
        if ta.crossover(f, s)[-1] and ctx.position(bar.inst) is None:
            return Signal.open_long(bar.inst, stop_loss="2atr", take_profit="4atr", reason="EMA cross up")
```

Handlers (all optional; each may return a `Signal`, a list of them, or `None`):

| handler | called |
|---|---|
| `on_start(ctx)` | once, after the process starts or restarts (`ctx.state` is restored) |
| `on_bar(ctx, bar)` | per instrument per confirmed bar of `timeframe`; `bar.inst bar.ts bar.o … bar.volq` |
| `on_universe_change(ctx, added, removed)` | when a Screener universe changes; `ctx.universe` is current |
| `on_fill(ctx, fill)` | a fill of this instance (`fill.inst_id`, `fill.pos_side`, `fill.qty`, `fill.px`, …) |
| `on_signal_result(ctx, result)` | `result.signal_id`, `result.outcome` (ordered, rejected, skipped_ai, …), `result.reason` |
| `on_funding(ctx, inst, rate, amount)` | funding settled on a position |
| `on_stop(ctx)` | before the process exits |

Each event must be handled within **5 s** (lockstep with the host) or the process is killed and the
instance marked faulted. An exception in a handler is logged and the event acknowledged; three in a
row pause the instance.

### Signals

- `Signal.open_long(inst, stop_loss=..., take_profit=..., strength=1.0, leverage=None, max_slippage_bp=None, ttl_ms=None, reason="")`, same for `open_short`
- `Signal.close(inst, reason="")` closes this instance's position in `inst`; `Signal.reduce(inst, frac, reason="")`
- `stop_loss` / `take_profit` take a price or `"<k>atr"` (k × ATR(14) on the strategy timeframe from the latest close)
- `strength` (0–1) scales the account's risk per trade; the order layer sizes the position from the stop distance.
  Accounts usually require a stop on every opening signal.
- One pending signal per instrument: to reverse, close first and open again from `on_fill` (see `library/majors_trend.py`).

### ctx

| member | returns |
|---|---|
| `ctx.bars(inst, tf=None, n=200)` | `Bars` with numpy arrays `.ts .o .h .l .c .vol .volq`, oldest first, confirmed bars only |
| `ctx.factors(inst)` | fact-panel values of the last confirmed minute (`dict`, missing → NaN) |
| `ctx.screener(rule, side, top, min_score, filter)` | pool rows (`inst_id`, `rank`, `long`, `short`, `values`) |
| `ctx.score(rule, inst)` | `(long, short)` rule scores 0–100, NaN when unscored |
| `ctx.position(inst)` / `ctx.positions()` | this instance's open positions (`pos_side`, `qty`, `entry_px`, `opened_at`, …) or `None` |
| `ctx.account()` / `ctx.spec(inst)` | account view / contract spec |
| `ctx.state` | a JSON-serializable dict (≤ 64 KB) saved after every event and restored on restart |
| `ctx.submit(signal)` | send a signal immediately (e.g. from `on_fill`); returns its id or `None` |
| `ctx.log(msg, level="info")` | goes to the server log with the instance prefix |
| `ctx.now`, `ctx.mode`, `ctx.params`, `ctx.universe` | host clock (ms), `live`/`backtest`, parameter values, current instruments |

Backtests replay the same events from recorded data, so use `ctx.now` instead of the wall clock and keep
everything you need across events in `ctx.state`.

### ta

Pine-like, numpy only, NaN-safe (warm-up and gaps give NaN, never an exception): `sma ema rma rsi atr tr
macd bbands crossover crossunder highest lowest stdev change`. EMA/RMA are seeded with the SMA of the first
`n` values. `bbands` returns `(middle, upper, lower)`, `macd` returns `(macd, signal, hist)`.

### Params

`Param(default, min=None, max=None, step=None, desc="", ai_tunable=False)`; read as `self.<name>`.
An int default yields ints. Values are clamped to `[min, max]`. `ai_tunable` lets the AI layer *propose*
changes, which only apply after the owner approves them.

## Limits

Each instance process runs with a 256 MB address-space limit (`CTG_MEM_LIMIT_MB`), a CPU-time limit
(`CTG_CPU_LIMIT_S`, default 6 h), one BLAS thread and, in Docker, no network and a read-only filesystem.
Only `numpy` and the standard library are available.

## Running locally

```sh
python3 strategies/runner.py                       # connects to data/run/strategy.sock (CTG_RUN_DIR / CTG_SOCKET)
cd strategies && python3 -m unittest discover -s tests
go test -tags pyintegration ./internal/strategyhost/  # Go host + real runner.py round trip
```
