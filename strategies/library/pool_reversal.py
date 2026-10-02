import math

from ctg import Param, Screener, Signal, Strategy

RULE = "resid-reversal"


class PoolReversal(Strategy):
    """Buys the pool's most oversold coins by the beta-neutral residual reversal rule and exits after a
    fixed holding time (or at the ATR stop). Unvalidated: the rule's track record decides."""

    name = "pool-reversal"
    description = "Dynamic pool: long the top residual-reversal names on 1H, exit after a fixed hold."
    universe = Screener(rule=RULE, side="long", top=10, refresh="1H")
    timeframe = "1H"

    min_score = Param(75.0, min=50, max=100, step=1, desc="Minimum long score (0-100) to enter", ai_tunable=True)
    hold_hours = Param(6, min=1, max=48, step=1, desc="Close after this many hours", ai_tunable=True)
    stop_atr = Param(2.5, min=0.5, max=6, step=0.25, desc="Stop distance in ATR(14)", ai_tunable=True)
    max_new = Param(2, min=1, max=10, step=1, desc="Max new entries per bar")

    def on_universe_change(self, ctx, added, removed):
        if added or removed:
            ctx.log(f"universe +{len(added)} -{len(removed)} = {len(ctx.universe)}")

    def on_bar(self, ctx, bar):
        out = []
        # Once per bar time, close every position held too long, including ones that left the universe.
        if ctx.state.get("swept") != bar.ts:
            ctx.state["swept"] = bar.ts
            ctx.state["entries"] = 0
            limit = ctx.now - self.hold_hours * 3600_000
            for p in ctx.positions():
                if p.opened_at and p.opened_at <= limit:
                    out.append(Signal.close(p.inst_id, reason=f"held {self.hold_hours}h"))
        if any(s.inst == bar.inst for s in out) or ctx.position(bar.inst) is not None:
            return out
        if ctx.state["entries"] >= self.max_new:
            return out
        long_score, _ = ctx.score(RULE, bar.inst)
        if math.isnan(long_score) or long_score < self.min_score:
            return out
        ctx.state["entries"] += 1
        out.append(Signal.open_long(bar.inst, stop_loss=f"{self.stop_atr}atr",
                                    reason=f"{RULE} long score {long_score:.1f} >= {self.min_score:g}"))
        return out
