from ctg import Param, Signal, Strategy


class DemoPulse(Strategy):
    """Demonstration only: opens a small BTC long on the first 1m bar after start, closes it after
    `hold` bars, waits `rest` bars and repeats. It exists to exercise the whole loop (signal → AI /
    confirmation → order → fill → Telegram) within minutes; it has no edge."""

    name = "demo-pulse"
    description = "演示用：每隔几分钟在 BTC 上开一笔小多单再平掉，用来体验信号、确认、成交和通知的完整流程，没有任何盈利逻辑。"
    universe = ["BTC-USDT-SWAP"]
    timeframe = "1m"

    hold = Param(3, min=1, max=60, step=1, desc="Bars to hold before closing")
    rest = Param(2, min=0, max=60, step=1, desc="Bars to wait after a close")
    stop_pct = Param(0.5, min=0.1, max=5, step=0.1, desc="Stop distance in percent of price")

    def on_start(self, ctx):
        ctx.state.setdefault("count", 0)

    def on_bar(self, ctx, bar):
        st = ctx.state
        st["count"] = st.get("count", 0) + 1
        pos = ctx.position(bar.inst)
        if pos is not None:
            st["held"] = st.get("held", 0) + 1
            if st["held"] >= self.hold:
                st["held"], st["rested"] = 0, 0
                return Signal.close(bar.inst, reason=f"演示：持有 {self.hold} 根后平仓")
            return None
        st["rested"] = st.get("rested", self.rest) + 1
        if st["rested"] <= self.rest:
            return None
        stop = bar.c * (1 - self.stop_pct / 100)
        return Signal.open_long(bar.inst, strength=0.2, stop_loss=stop, take_profit=bar.c * (1 + 2 * self.stop_pct / 100),
                                ttl_ms=120_000, reason=f"演示：第 {st['count']} 根 1m K 线开多")
