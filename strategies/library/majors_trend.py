# 内置策略：大币均线趋势（只读示例，可在编辑器里「复制为新策略」后修改）。
#
# 思路：趋势跟随。快速均线（EMA20）在慢速均线（EMA50）上方，说明近期涨势强于长期，看多；
# 在下方则看空。持仓方向始终跟着均线方向走，方向一变就反手。
#
# 两种入场方式（参数 entry）：
#   state  只要方向成立、手里没有仓位就入场。新建实例后下一根 K 线就可能开仓，
#          但为了不在涨了很久之后追高，价格离快线太远（超过 max_chase 个 ATR）时先等回落。
#   cross  只在两条均线刚交叉的那一根 K 线入场。信号最少、最“干净”，
#          但错过那一根（实例刚建、暂停、重启）就要等下一次交叉，可能是几周。
#
# 本文件的注释是中文的，这是仓库“全部用英文”约定的例外：内置策略也是给主人看的教材。
from ctg import Bool, Choice, Float, Int, Signal, Strategy, Timeframe, ta


class MajorsTrend(Strategy):
    """BTC/ETH EMA trend follower: long while the fast EMA is above the slow one, short below."""

    # 策略的唯一标识。复制后要改成别的名字（编辑器会自动加 -copy）。
    name = "majors-trend"
    # 显示在策略卡片上的一句话说明。
    description = "BTC/ETH 均线趋势：快线在慢线上方做多、下方做空；方向反转时一步反手。可选「方向成立即入场」或「只在交叉时入场」。"
    # 交易哪些合约。实例创建时可以改。
    universe = ["BTC-USDT-SWAP", "ETH-USDT-SWAP"]
    # K 线周期也做成参数：每个实例可以选 1H / 4H / 1D。
    timeframe = Timeframe("4H", options=["1H", "4H", "1D"], group="信号")
    # 计算需要的历史 K 线数量（慢线长度的 3 倍，让 EMA 充分收敛）。
    warmup = 150

    # ---- 参数：在实例表单里按 group 分组显示，desc 是问号提示 ----
    fast = Int(20, min=5, max=100, group="信号", label="快线长度", desc="快速 EMA 的 K 线根数", ai_tunable=True)
    slow = Int(50, min=20, max=300, group="信号", label="慢线长度", desc="慢速 EMA 的 K 线根数，需大于快线", ai_tunable=True)
    entry = Choice("state", ["state", "cross"], labels=["方向成立即入场", "只在交叉那根入场"], group="信号",
                   label="入场方式", desc="state：没有仓位且方向成立就入场；cross：只在均线刚交叉的那根 K 线入场")
    max_chase = Float(1.5, min=0, max=5, step=0.25, group="信号", label="追高上限（ATR）",
                      desc="state 方式下，收盘价离快线超过这么多个 ATR(14) 就先不入场，等价格回落；0 = 不限制")
    direction = Choice("both", ["both", "long", "short"], labels=["多空双向", "只做多", "只做空"], group="风控",
                       label="方向", desc="只做多时，看空信号只会平掉多单，不会开空单")
    stop_atr = Float(2.0, min=0.5, max=6, step=0.25, group="风控", label="止损（ATR）",
                     desc="止损距离 = 这么多个 ATR(14)。下单数量也按它算：止损越远，仓位越小", ai_tunable=True)
    target_atr = Float(4.0, min=0, max=12, step=0.5, group="风控", label="止盈（ATR）", desc="0 = 不设止盈，只靠反手或止损出场",
                       ai_tunable=True)
    trail_atr = Float(0, min=0, max=6, step=0.25, group="风控", label="追踪止损（ATR）",
                      desc="止损跟随最有利价格移动，只收紧不放松；0 = 关闭")
    notify = Bool(True, group="通知", label="入场和反手时发消息", desc="通过 Telegram / Webhook 通知（在设置里配置通道）")

    def on_start(self, ctx):
        # 实例启动（或重启）时调用一次。ctx.log 写进实例的运行日志，卡片上能看到。
        ctx.log(f"启动：EMA{self.fast}/{self.slow} · {self.timeframe} · 入场方式 {self.entry} · 方向 {self.direction}")

    def _trend(self, ctx, inst):
        """读取 K 线算出 (方向, 是否刚交叉, 收盘价, 快线, 慢线, ATR)，数据不够时返回 None。"""
        b = ctx.bars(inst, n=self.slow * 3)  # 不写周期 = 用实例的周期
        if len(b) < self.slow + 2:
            return None
        f, s = ta.ema(b.c, self.fast), ta.ema(b.c, self.slow)
        atr = ta.atr(b.h, b.l, b.c, 14)[-1]
        side = "long" if f[-1] > s[-1] else "short"
        return side, bool(ta.cross(f, s)[-1]), float(b.c[-1]), float(f[-1]), float(s[-1]), float(atr)

    def on_bar(self, ctx, bar):
        # 每个品种、每根收盘的 K 线调用一次。返回 Signal（或 None = 不操作）。
        if self.fast >= self.slow:
            return None  # 参数不合理：status() 会在卡片上提示
        t = self._trend(ctx, bar.inst)
        if t is None:
            return None
        want, crossed, close, fast, _slow, atr = t
        pos = ctx.position(bar.inst)
        held = pos.pos_side if pos else None
        if held == want:
            return None  # 已经站在趋势一边，什么都不做

        if crossed:
            why = f"EMA{self.fast} {'上穿' if want == 'long' else '下穿'} EMA{self.slow}（{self.timeframe}）"
        else:
            why = f"EMA{self.fast} 在 EMA{self.slow} {'上方' if want == 'long' else '下方'}（{self.timeframe}）"
        if not self.allows(want):
            # 新方向被禁用（例如“只做多”时出现看空）：只把旧仓位平掉
            return Signal.close(bar.inst, reason=f"{why}，不做{'多' if want == 'long' else '空'}，平仓") if held else None

        if held is None:
            # 没有仓位时才看入场条件；有反向仓位时总是反手（持仓要跟着趋势走）
            if self.entry == "cross" and not crossed:
                return None
            if self.entry == "state" and self.max_chase > 0 and atr > 0 and abs(close - fast) > self.max_chase * atr:
                return None  # 离快线太远，不追

        sig = Signal.open(
            bar.inst, want,
            stop_loss=f"{self.stop_atr}atr",                                      # 止损：入场价 ∓ k×ATR
            take_profit=f"{self.target_atr}atr" if self.target_atr > 0 else None,  # 止盈（可关）
            trailing_stop=f"{self.trail_atr}atr" if self.trail_atr > 0 else None,  # 追踪止损（可关）
            reason=why,
        )
        if held:
            # 一步反手：订单层先平掉反向仓位，成交后立刻开新方向（不受平仓冷却限制）
            sig = sig.reversed()
        if self.notify:
            ctx.notify(f"{bar.inst.replace('-USDT-SWAP', '')} {'反手' if held else ''}{'开多' if want == 'long' else '开空'}"
                       f" @ {close:g}：{why}", price=close)
        return sig

    def on_signal_result(self, ctx, result):
        # 信号被拒绝或跳过时记一条日志，方便在卡片上排查“为什么没成交”
        if result.outcome not in ("ordered", "recorded", "pending"):
            ctx.log(f"信号 #{result.signal_id} {result.outcome}：{result.reason}", level="warn")

    def status(self, ctx):
        # 实例卡片上显示的一行状态：每个品种当前的均线方向和持仓
        if self.fast >= self.slow:
            return f"参数错误：快线 {self.fast} 需小于慢线 {self.slow}"
        parts = []
        for inst in ctx.universe:
            t = self._trend(ctx, inst)
            name = inst.replace("-USDT-SWAP", "")
            if t is None:
                parts.append(f"{name} 数据不足")
                continue
            want, _, close, fast, slow, atr = t
            pos = ctx.position(inst)
            gap = (fast / slow - 1) * 100
            s = f"{name} 快线{'在上' if want == 'long' else '在下'} {gap:+.2f}%"
            if pos:
                s += f"，持{'多' if pos.pos_side == 'long' else '空'}"
            elif not self.allows(want):
                s += f"，不做{'多' if want == 'long' else '空'}，空仓等待"
            elif self.entry == "cross":
                s += "，等下一次交叉"
            elif self.max_chase > 0 and atr > 0 and abs(close - fast) > self.max_chase * atr:
                s += "，离快线太远，等回落"
            else:
                s += "，下一根收盘入场"
            parts.append(s)
        return "；".join(parts)
