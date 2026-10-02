from ctg import Param, Signal, Strategy, ta


class MajorsTrend(Strategy):
    """Trend following on BTC and ETH: long when the fast EMA crosses above the slow one on 4H, short on
    the opposite cross, ATR-based stop and target. An opposite cross first closes the position and
    re-enters after the close fills."""

    name = "majors-trend"
    description = "BTC/ETH 4H EMA cross trend follower with ATR(14) stop and target."
    universe = ["BTC-USDT-SWAP", "ETH-USDT-SWAP"]
    timeframe = "4H"

    fast = Param(20, min=5, max=100, step=1, desc="Fast EMA length (bars)", ai_tunable=True)
    slow = Param(50, min=20, max=300, step=1, desc="Slow EMA length (bars)", ai_tunable=True)
    stop_atr = Param(2.0, min=0.5, max=6, step=0.25, desc="Stop distance in ATR(14)", ai_tunable=True)
    target_atr = Param(4.0, min=1, max=12, step=0.5, desc="Target distance in ATR(14)", ai_tunable=True)

    def on_start(self, ctx):
        ctx.state.setdefault("reenter", {})
        ctx.log(f"started fast={self.fast} slow={self.slow} mode={ctx.mode}")

    def _open(self, inst, side, why):
        levels = dict(stop_loss=f"{self.stop_atr}atr", take_profit=f"{self.target_atr}atr", reason=why)
        return Signal.open_long(inst, **levels) if side == "long" else Signal.open_short(inst, **levels)

    def on_bar(self, ctx, bar):
        if self.fast >= self.slow:
            return None
        b = ctx.bars(bar.inst, self.timeframe, self.slow * 3)
        if len(b) < self.slow + 2:
            return None
        f, s = ta.ema(b.c, self.fast), ta.ema(b.c, self.slow)
        if ta.crossover(f, s)[-1]:
            side = "long"
        elif ta.crossunder(f, s)[-1]:
            side = "short"
        else:
            return None
        why = f"EMA{self.fast} crossed {'above' if side == 'long' else 'below'} EMA{self.slow} on {self.timeframe}"
        pos = ctx.position(bar.inst)
        if pos is None:
            return self._open(bar.inst, side, why)
        if pos.pos_side == side:
            return None
        # Reverse in two steps: the order layer allows one pending signal per instrument.
        ctx.state["reenter"][bar.inst] = {"side": side, "why": why, "until": ctx.now + 8 * 3600_000}
        return Signal.close(bar.inst, reason=f"{why}; closing {pos.pos_side}")

    def on_fill(self, ctx, fill):
        want = ctx.state["reenter"].pop(fill.inst_id, None)
        if want and ctx.now <= want["until"] and ctx.position(fill.inst_id) is None:
            ctx.submit(self._open(fill.inst_id, want["side"], want["why"]))

    def on_signal_result(self, ctx, result):
        if result.outcome not in ("ordered", "recorded", "pending"):
            ctx.log(f"signal {result.signal_id} {result.outcome}: {result.reason}", level="warn")
