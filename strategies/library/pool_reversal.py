# 内置策略：币池残差反转（只读示例，可在编辑器里「复制为新策略」后修改）。
#
# 残差反转评分（resid-reversal）回答的是“哪些币相对大盘跌/涨过头了”，不回答“现在该不该买”。
# 跌得多的币可能继续跌（接飞刀），所以评分只用来挑候选，入场要等价格本身给出止跌/止涨的证据。
# 整个策略分五层，每层都有参数：
#
#   1 评分筛选  每小时按评分取多空两侧的候选（分数 ≥ min_score；规则自带点差门槛）
#   2 观察名单  候选进入名单并记录时间；超过 setup_hours 还没触发，或分数掉下去，就移出
#   3 触发确认  在本周期（默认 15m）K 线上找“跌势衰竭”的证据，满足 need 项才入场：
#                 价格收复 EMA9 · RSI 从 30 下方拐回（做空看 70） · 主动买卖净额转向 · OI 下降（杠杆出清）
#               另有 BTC 否决：BTC 最近 1 小时反向急动时不开新仓
#   4 组合约束  已触发的候选按评分排序，受总持仓数和单侧持仓数限制，评分高的先上
#   5 出场      持仓时限（订单层到点平仓）、评分回落到 exit_score 以下（残差已回归）、ATR 止损、可选追踪止损
#
# 多空是对称的：做空侧看“涨过头 + 止涨证据”。
# 本文件的注释是中文的，这是仓库“全部用英文”约定的例外：内置策略也是给主人看的教材。
import math

from ctg import Bool, Choice, Float, Int, Screener, Signal, Strategy, Timeframe, Watchlist, pick, ta

RULE = "resid-reversal"
BTC = "BTC-USDT-SWAP"
WATCH = Watchlist("watch")  # 观察名单存在 ctx.state["watch"]，重启不丢


class PoolReversal(Strategy):
    """Residual reversal on the dynamic pool: the score picks candidates, an intrabar trigger confirms
    exhaustion before entering, positions are allocated by score within limits."""

    name = "pool-reversal"
    description = "残差反转（多空）：评分只用来挑候选，等 15m 级别出现止跌/止涨确认才入场，按评分分配名额，到时或分数回归就离场。"
    # 动态币池：每小时取评分（多空较强一侧）最高的 10 个，这些币每根 K 线都会送进来
    universe = Screener(rule=RULE, side="both", top=10, refresh="1H")
    timeframe = Timeframe("15m", options=["5m", "15m", "1H"], group="3 触发")
    warmup = 60

    # ---- 1 评分筛选 ----
    min_score = Float(80, min=50, max=100, step=1, group="1 筛选", label="最低评分",
                      desc="评分 0–100，是在整个币池里的百分位排名；80 ≈ 前 20%", ai_tunable=True)
    # ---- 2 观察名单 ----
    setup_hours = Float(4, min=1, max=24, step=0.5, group="2 观察", label="观察有效期（小时）",
                        desc="入选后这么久还没触发就放弃")
    # ---- 3 触发确认 ----
    need = Int(2, min=1, max=4, group="3 触发", label="至少满足几项", desc="下面勾选的确认信号里，至少满足几项才入场")
    use_reclaim = Bool(True, group="3 触发", label="价格收复 EMA9", desc="做多：最近 2 根内收盘上穿 EMA9；做空：下穿")
    use_rsi = Bool(True, group="3 触发", label="RSI 拐头", desc="做多：RSI(14) 最近 2 根内从 30 下方回到上方；做空：从 70 上方回落")
    use_flow = Bool(True, group="3 触发", label="主动买卖转向", desc="做多：最近一根主动买入净额转正（之前为负）；做空相反")
    use_oi = Bool(True, group="3 触发", label="OI 下降（杠杆出清）", desc="最近 4 小时持仓量下降 ≥ 0.5%：被迫离场的单子已经出来了")
    btc_veto_atr = Float(1.5, min=0, max=5, step=0.25, group="3 触发", label="BTC 否决（1H ATR）",
                         desc="BTC 最近 1 小时反向波动超过这么多个 ATR 时不开新仓；0 = 关闭")
    # ---- 4 组合约束 ----
    direction = Choice("both", ["both", "long", "short"], labels=["多空双向", "只做多", "只做空"], group="4 组合", label="方向")
    max_pos = Int(4, min=1, max=10, group="4 组合", label="最多同时持仓")
    max_side = Int(3, min=1, max=10, group="4 组合", label="单侧最多持仓")
    # ---- 5 出场 ----
    hold_hours = Float(6, min=1, max=48, step=0.5, group="5 出场", label="持仓时限（小时）", ai_tunable=True)
    exit_score = Float(55, min=0, max=100, step=1, group="5 出场", label="评分回落离场",
                       desc="持仓方向的评分掉到这以下就离场（超跌已修复）；0 = 不用")
    stop_atr = Float(2.5, min=0.5, max=6, step=0.25, group="5 出场", label="止损（ATR）", ai_tunable=True)
    trail_atr = Float(0, min=0, max=6, step=0.25, group="5 出场", label="追踪止损（ATR）", desc="0 = 关闭")
    notify = Bool(False, group="通知", label="每次入场发消息")

    def on_bars(self, ctx, bars):
        # 重写 on_bars：每个 K 线时刻只调用一次，一次拿到所有品种（bars = {品种: Bar}），
        # 这样才能在品种之间比较、排序、分配名额。
        out = self._exits(ctx)                                        # 第 5 层：评分回归离场

        cands = ctx.rank(RULE, side=self.direction, min_score=self.min_score)  # 第 1 层：全池按评分排序
        WATCH.sync(ctx, cands, ttl_ms=int(self.setup_hours * 3600_000))  # 第 2 层：进出观察名单

        veto = self._btc_veto(ctx)
        ready = []
        for c in WATCH.items(ctx):                                    # 第 3 层：逐个检查触发条件
            if c.inst_id not in bars or ctx.position(c.inst_id) is not None or not self.allows(c.side):
                continue
            hits = self._confirmations(ctx, c.inst_id, c.side)
            WATCH.update(ctx, c.inst_id, hits=hits)
            if c.side in veto:
                continue
            if len(hits) >= min(self.need, self._enabled()):
                c["hits"] = hits
                ready.append(c)

        for c in pick(ready, ctx.positions(), self.max_pos, self.max_side):  # 第 4 层：按评分分配名额
            reason = f"{RULE} {'多' if c.side == 'long' else '空'} {c.score:.0f} 分，确认：{'、'.join(c.hits)}"
            out.append(Signal.open(
                c.inst_id, c.side,
                stop_loss=f"{self.stop_atr}atr",
                trailing_stop=f"{self.trail_atr}atr" if self.trail_atr > 0 else None,
                time_limit=f"{self.hold_hours}h",                     # 到时由订单层平仓
                reason=reason,
            ))
            WATCH.remove(ctx, c.inst_id)
            if self.notify:
                ctx.notify(f"{c.inst_id.replace('-USDT-SWAP', '')} {'开多' if c.side == 'long' else '开空'}：{reason}")
        return out

    def _enabled(self) -> int:
        return sum((self.use_reclaim, self.use_rsi, self.use_flow, self.use_oi)) or 1

    def _confirmations(self, ctx, inst, side) -> list[str]:
        """返回已满足的确认项名称。做空是做多的镜像。"""
        long = side == "long"
        hits = []
        b = ctx.bars(inst, n=60)
        if len(b) >= 30:
            if self.use_reclaim:
                e9 = ta.ema(b.c, 9)
                x = ta.crossover(b.c, e9) if long else ta.crossunder(b.c, e9)
                if x[-2:].any():
                    hits.append("收复EMA9" if long else "跌破EMA9")
            if self.use_rsi:
                r = ta.rsi(b.c, 14)
                x = ta.crossover(r, 30) if long else ta.crossunder(r, 70)
                if x[-2:].any():
                    hits.append("RSI拐头")
        if self.use_flow:
            d = ctx.series(inst, "delta", n=4)  # 主动买入 − 主动卖出（USDT），按本周期分桶
            if len(d) >= 3 and math.isfinite(d.v[-1]):
                turned = (d.v[-1] > 0 and min(d.v[-3:-1]) < 0) if long else (d.v[-1] < 0 and max(d.v[-3:-1]) > 0)
                if turned:
                    hits.append("主动买入转正" if long else "主动卖出转强")
        if self.use_oi:
            oi = ctx.series(inst, "oi", tf="1H", n=5)
            if len(oi) >= 4 and oi.v[0] > 0 and oi.v[-1] / oi.v[0] - 1 <= -0.005:
                hits.append("OI下降")
        return hits

    def _btc_veto(self, ctx) -> set[str]:
        """BTC 最近 1 小时急跌时不做多、急涨时不做空。"""
        if self.btc_veto_atr <= 0:
            return set()
        b = ctx.bars(BTC, "1H", 30)
        if len(b) < 20:
            return set()
        atr = ta.atr(b.h, b.l, b.c, 14)[-1]
        move = b.c[-1] - b.c[-2]
        if not math.isfinite(atr) or atr <= 0:
            return set()
        if move < -self.btc_veto_atr * atr:
            return {"long"}
        if move > self.btc_veto_atr * atr:
            return {"short"}
        return set()

    def _exits(self, ctx) -> list:
        """评分回归离场：持仓方向的评分跌破 exit_score，说明超跌/超涨已经修复。"""
        if self.exit_score <= 0:
            return []
        out = []
        for p in ctx.positions():
            long_s, short_s = ctx.score(RULE, p.inst_id)
            s = long_s if p.pos_side == "long" else short_s
            if math.isfinite(s) and s < self.exit_score:
                out.append(Signal.close(p.inst_id, reason=f"{RULE} 评分回落到 {s:.0f}（< {self.exit_score:g}），残差已回归"))
        return out

    def on_signal_result(self, ctx, result):
        if result.outcome not in ("ordered", "recorded", "pending"):
            ctx.log(f"信号 #{result.signal_id} {result.outcome}：{result.reason}", level="warn")

    def status(self, ctx):
        items = WATCH.items(ctx)
        held = len(ctx.positions())
        if not items:
            return f"观察名单为空（没有评分 ≥ {self.min_score:g} 的候选）；持仓 {held}/{self.max_pos}"
        top = []
        for c in items[:4]:
            hits = c.get("hits") or []
            top.append(f"{c.inst_id.replace('-USDT-SWAP', '')}{'多' if c.side == 'long' else '空'}{c.score:.0f}"
                       f"（{len(hits)}/{min(self.need, self._enabled())}）")
        more = f" 等 {len(items)} 个" if len(items) > 4 else ""
        return f"观察 {'、'.join(top)}{more}；持仓 {held}/{self.max_pos}"
