/** Starting points for a new strategy (shown when creating one). Comments are Chinese on purpose: they teach. */
export interface Template { id: string; title: string; desc: string; code: string }

export const templates: Template[] = [
  {
    id: 'trend',
    title: '单品种趋势',
    desc: '均线方向成立就入场、方向反转就反手。最常见的入门写法。',
    code: `from ctg import Choice, Float, Int, Signal, Strategy, Timeframe, ta


class MyTrend(Strategy):
    """均线趋势：快线在慢线上方做多，下方做空。"""

    name = "my-trend"                      # 唯一标识：小写字母、数字和 -
    description = "我的均线趋势策略"
    universe = ["BTC-USDT-SWAP"]           # 交易哪些合约（实例可改）
    timeframe = Timeframe("1H")            # K 线周期（实例可改）

    fast = Int(20, min=5, max=100, label="快线", desc="快速 EMA 长度")
    slow = Int(60, min=20, max=300, label="慢线", desc="慢速 EMA 长度")
    direction = Choice("both", ["both", "long", "short"], labels=["多空", "只做多", "只做空"], label="方向")
    stop_atr = Float(2.0, min=0.5, max=6, step=0.25, label="止损（ATR）")

    def on_bar(self, ctx, bar):
        # 每根收盘的 K 线调用一次
        b = ctx.bars(bar.inst, n=self.slow * 3)
        if len(b) < self.slow + 2:
            return None                    # 历史数据还不够
        f, s = ta.ema(b.c, self.fast), ta.ema(b.c, self.slow)
        want = "long" if f[-1] > s[-1] else "short"
        pos = ctx.position(bar.inst)
        if pos and pos.pos_side == want:
            return None                    # 已经在趋势一边
        if not self.allows(want):
            return Signal.close(bar.inst, reason="方向不允许，平仓") if pos else None
        sig = Signal.open(bar.inst, want, stop_loss=f"{self.stop_atr}atr", reason=f"EMA{self.fast} 与 EMA{self.slow} 方向：{want}")
        return sig.reversed() if pos else sig   # 有反向仓位就一步反手

    def status(self, ctx):
        return f"监控 {len(ctx.universe)} 个品种，持仓 {len(ctx.positions())} 个"
`,
  },
  {
    id: 'portfolio',
    title: '多币种评分组合',
    desc: '用评分挑候选、等确认、按名额分配。适合在动态币池里做多空。',
    code: `from ctg import Float, Int, Screener, Signal, Strategy, Watchlist, pick, ta

RULE = "resid-reversal"
WATCH = Watchlist()


class MyPortfolio(Strategy):
    """评分只挑候选，入场要等 RSI 拐头确认。"""

    name = "my-portfolio"
    description = "评分挑候选 + RSI 确认 + 按评分分配名额"
    universe = Screener(rule=RULE, side="both", top=10, refresh="1H")
    timeframe = "15m"

    min_score = Float(80, min=50, max=100, label="最低评分")
    max_pos = Int(3, min=1, max=10, label="最多持仓")

    def on_bars(self, ctx, bars):
        # 一次拿到所有品种的 K 线（bars = {品种: Bar}），可以横向比较
        WATCH.sync(ctx, ctx.rank(RULE, side="both", min_score=self.min_score), ttl_ms=4 * 3600_000)
        ready = []
        for c in WATCH.items(ctx):
            if c.inst_id not in bars or ctx.position(c.inst_id):
                continue
            r = ta.rsi(ctx.bars(c.inst_id, n=60).c, 14)
            turned = ta.crossover(r, 30)[-1] if c.side == "long" else ta.crossunder(r, 70)[-1]
            if turned:
                ready.append(c)
        out = []
        for c in pick(ready, ctx.positions(), max_total=self.max_pos):
            out.append(Signal.open(c.inst_id, c.side, stop_loss="2.5atr", time_limit="6h", reason=f"评分 {c.score:.0f} + RSI 拐头"))
            WATCH.remove(ctx, c.inst_id)
        return out
`,
  },
  {
    id: 'notify',
    title: '定时检查 + 发通知',
    desc: '不交易，只按时间检查行情并把结果发到 Telegram / Webhook。',
    code: `from ctg import Float, Strategy, ta


class MyWatcher(Strategy):
    """每小时检查一次，价格偏离均线太远就发通知。不下单。"""

    name = "my-watcher"
    description = "偏离均线提醒（只发通知，不交易）"
    universe = ["BTC-USDT-SWAP", "ETH-USDT-SWAP"]
    timeframe = "1H"
    schedule = {"check": "1H"}             # 每小时调用一次 on_timer(ctx, "check")
    can_long = can_short = False           # 声明不交易

    gap_pct = Float(3.0, min=0.5, max=20, step=0.5, label="偏离阈值（%）")

    def on_timer(self, ctx, name):
        for inst in ctx.universe:
            b = ctx.bars(inst, n=100)
            if len(b) < 60:
                continue
            gap = (b.c[-1] / ta.ema(b.c, 50)[-1] - 1) * 100
            if abs(gap) >= self.gap_pct:
                ctx.notify(f"{inst} 偏离 EMA50 {gap:+.1f}%", price=float(b.c[-1]))
`,
  },
  {
    id: 'blank',
    title: '空白',
    desc: '只有骨架，从零开始写。',
    code: `from ctg import Signal, Strategy, ta


class MyStrategy(Strategy):
    """一句话说明策略在做什么。"""

    name = "my-strategy"
    universe = ["BTC-USDT-SWAP"]
    timeframe = "1H"

    def on_bar(self, ctx, bar):
        return None
`,
  },
]
