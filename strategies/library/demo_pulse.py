# 内置策略：演示脉冲（只读示例）。没有任何盈利逻辑，只用来几分钟内走完整个流程：
# 信号 → （AI 审核 / 人工确认）→ 下单 → 成交 → 通知。
# 本文件的注释是中文的，这是仓库“全部用英文”约定的例外：内置策略也是给主人看的教材。
from ctg import Float, Int, Signal, Strategy


class DemoPulse(Strategy):
    """Demonstration only: opens a small BTC long, closes it after `hold` bars, waits `rest` bars, repeats."""

    name = "demo-pulse"
    description = "演示用：每隔几分钟在 BTC 上开一笔小多单再平掉，用来体验信号、确认、成交和通知的完整流程，没有任何盈利逻辑。"
    universe = ["BTC-USDT-SWAP"]
    timeframe = "1m"
    can_short = False  # 只开多单

    hold = Int(3, min=1, max=60, label="持有根数", desc="开仓后持有几根 1m K 线再平仓")
    rest = Int(2, min=0, max=60, label="间隔根数", desc="平仓后等几根再开下一笔")
    stop_pct = Float(0.5, min=0.1, max=5, step=0.1, label="止损（%）", desc="止损距离，占价格的百分比；止盈是它的 2 倍")

    def on_start(self, ctx):
        # ctx.state 是持久化的字典：每个事件后自动保存，重启后恢复
        ctx.state.setdefault("count", 0)

    def on_position_closed(self, ctx, trade):
        # 不管是策略自己平的、还是止损 / 止盈打掉的，都从头计数，并记下平仓时间
        ctx.state.update(held=0, rested=0, closed_at=trade.ts)

    def on_bar(self, ctx, bar):
        st = ctx.state
        st["count"] = st.get("count", 0) + 1
        if ctx.position(bar.inst) is not None:
            st["held"] = st.get("held", 0) + 1
            if st["held"] >= self.hold:
                return Signal.close(bar.inst, reason=f"演示：持有 {self.hold} 根后平仓")
            return None
        st["rested"] = st.get("rested", self.rest) + 1
        if st["rested"] <= self.rest:
            return None
        # 账户有「平仓后 N 分钟内不再开仓」的冷却（默认 30 分钟）。读出来等它过去，免得每分钟发一个会被拒的信号
        acc = ctx.account()
        cooldown = (acc.limits.cooldown_min if acc else 0) * 60_000
        if ctx.now - st.get("closed_at", 0) < cooldown:
            return None
        # strength=0.2：只用账户单笔风险额度的 20%；止损/止盈用百分比写法
        return Signal.open_long(bar.inst, strength=0.2, stop_loss=f"{self.stop_pct}%", take_profit=f"{2 * self.stop_pct}%",
                                ttl_ms=120_000, reason=f"演示：第 {st['count']} 根 1m K 线开多")

    def status(self, ctx):
        if ctx.position("BTC-USDT-SWAP"):
            return f"持多 {ctx.state.get('held', 0)}/{self.hold} 根"
        acc = ctx.account()
        wait = (acc.limits.cooldown_min if acc else 0) * 60_000 - (ctx.now - ctx.state.get("closed_at", 0))
        if wait > 0:
            return f"空仓，等平仓冷却结束（约 {wait // 60_000 + 1} 分钟）"
        return f"空仓，{max(0, self.rest - ctx.state.get('rested', 0) + 1)} 根后开仓"
