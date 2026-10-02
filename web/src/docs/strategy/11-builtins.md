---
title: 内置策略讲解
summary: 逐段讲解三个内置策略：演示脉冲、大币均线趋势、币池残差反转。
---

# 内置策略讲解

内置策略在 `strategies/library/` 下，注释是中文的，本身就是教材。它们在编辑器里是只读的：想改就点 **复制为新策略**，得到一份名字带 `-copy` 的草稿。

| 策略 | 难度 | 学什么 |
|---|---|---|
| `demo-pulse` 演示脉冲 | 入门 | `ctx.state`、`on_position_closed`、读账户规则、百分比止损、`strength`，走通“信号 → 成交 → 通知”流程 |
| `majors-trend` 大币均线趋势 | 进阶 | 参数分组、`Timeframe` 参数、`allows()`、一步反手、`status()` |
| `pool-reversal` 币池残差反转 | 高级 | `Screener`、`on_bars`、`ctx.rank`、`Watchlist`、`pick`、`ctx.series` |

## 演示脉冲 demo-pulse

没有任何盈利逻辑：在 BTC 上开一笔小多单，持有几分钟后平掉，等几分钟再来。用来几分钟内看到完整流程。

```python
universe = ["BTC-USDT-SWAP"]
timeframe = "1m"
can_short = False  # 只开多单

hold = Int(3, min=1, max=60, label="持有根数", desc="开仓后持有几根 1m K 线再平仓")
rest = Int(2, min=0, max=60, label="间隔根数", desc="平仓后等几根再开下一笔")
stop_pct = Float(0.5, min=0.1, max=5, step=0.1, label="止损（%）", desc="止损距离，占价格的百分比；止盈是它的 2 倍")
```

**用 ctx.state 做计数器**。`on_start` 里初始化，`on_bar` 里每根 K 线累加：

```python
def on_start(self, ctx):
    ctx.state.setdefault("count", 0)

def on_bar(self, ctx, bar):
    st = ctx.state
    st["count"] = st.get("count", 0) + 1
    if ctx.position(bar.inst) is not None:
        st["held"] = st.get("held", 0) + 1
        if st["held"] >= self.hold:
            return Signal.close(bar.inst, reason=f"演示：持有 {self.hold} 根后平仓")
        return None
```

有仓位时数“持有了几根”，到了就发平仓信号。

**在 on_position_closed 里清零**。仓位可能是策略自己平的，也可能是止损 / 止盈打掉的，两种情况都会调用 `on_position_closed`，所以清零放在这里最可靠，顺便记下平仓时间：

```python
def on_position_closed(self, ctx, trade):
    ctx.state.update(held=0, rested=0, closed_at=trade.ts)
```

**没有仓位时数“休息了几根”，再等冷却结束**，然后开仓：

```python
    st["rested"] = st.get("rested", self.rest) + 1
    if st["rested"] <= self.rest:
        return None
    acc = ctx.account()
    cooldown = (acc.limits.cooldown_min if acc else 0) * 60_000
    if ctx.now - st.get("closed_at", 0) < cooldown:
        return None
    return Signal.open_long(bar.inst, strength=0.2, stop_loss=f"{self.stop_pct}%",
                            take_profit=f"{2 * self.stop_pct}%", ttl_ms=120_000, reason=...)
```

- `st.get("rested", self.rest)`：第一次运行时直接视为“休息够了”，所以启动后第一根就开仓。
- **读账户规则**：账户有“平仓后 N 分钟内不再开同一合约”的冷却（默认 30 分钟）。策略用 `ctx.account().limits.cooldown_min` 读出来自己等，而不是每分钟发一个注定被拒的信号。在默认设置下，一轮大约是“持有 3 分钟 + 冷却 30 分钟”；把演示账户的冷却时间调短，一轮就更快。
- `strength=0.2`：只用单笔风险额度的 20%，仓位很小。
- `"0.5%"`：百分比写法的止损，止盈是 `"1.0%"`。
- `ttl_ms=120_000`：信号 2 分钟内有效。开启人工确认时，2 分钟内不回答就过期。

`status()` 会显示 `持多 2/3 根`、`空仓，等平仓冷却结束（约 27 分钟）` 或 `空仓，1 根后开仓`。

## 大币均线趋势 majors-trend

经典趋势跟随：EMA20 在 EMA50 上方看多，下方看空，持仓方向始终跟着均线走，方向一变就反手。

### 声明

```python
universe = ["BTC-USDT-SWAP", "ETH-USDT-SWAP"]
timeframe = Timeframe("4H", options=["1H", "4H", "1D"], group="信号")
warmup = 150
```

周期做成了参数，每个实例可以选 1H / 4H / 1D。参数分三组：

| 分组 | 参数 | 说明 |
|---|---|---|
| 信号 | `fast` `slow` | EMA 长度，默认 20 / 50，AI 可调 |
| 信号 | `entry` | `state` 方向成立即入场（默认），`cross` 只在交叉那根入场 |
| 信号 | `max_chase` | `state` 方式下收盘价离快线超过几个 ATR 就先不追，0 = 不限制 |
| 风控 | `direction` | 多空双向 / 只做多 / 只做空 |
| 风控 | `stop_atr` `target_atr` `trail_atr` | 止损 / 止盈 / 追踪止损的 ATR 倍数，后两个为 0 表示关闭 |
| 通知 | `notify` | 入场和反手时发消息 |

### 把计算抽成一个函数

```python
def _trend(self, ctx, inst):
    b = ctx.bars(inst, n=self.slow * 3)  # 不写周期 = 用实例的周期
    if len(b) < self.slow + 2:
        return None
    f, s = ta.ema(b.c, self.fast), ta.ema(b.c, self.slow)
    atr = ta.atr(b.h, b.l, b.c, 14)[-1]
    side = "long" if f[-1] > s[-1] else "short"
    return side, bool(ta.cross(f, s)[-1]), float(b.c[-1]), float(f[-1]), float(s[-1]), float(atr)
```

`on_bar` 和 `status()` 都要用同样的计算，抽成 `_trend` 避免两处写法不一致。读 `slow × 3` 根是为了让 EMA 充分收敛；历史不够就返回 None。

### on_bar 的判断顺序

```python
if self.fast >= self.slow:
    return None  # 参数不合理：status() 会在卡片上提示
...
pos = ctx.position(bar.inst)
held = pos.pos_side if pos else None
if held == want:
    return None  # 已经站在趋势一边，什么都不做
```

1. 参数之间的约束（快线 < 慢线）表单拦不住，代码里判断。
2. 已经持有趋势方向的仓位：什么都不做。

```python
if not self.allows(want):
    return Signal.close(bar.inst, reason=...) if held else None
```

3. 新方向被禁用（比如“只做多”时出现看空）：只把旧仓位平掉，不开反向。

```python
if held is None:
    if self.entry == "cross" and not crossed:
        return None
    if self.entry == "state" and self.max_chase > 0 and atr > 0 and abs(close - fast) > self.max_chase * atr:
        return None  # 离快线太远，不追
```

4. 只有**空仓**时才检查入场方式。手里有反向仓位时总是反手，因为持仓要跟着趋势走。

```python
sig = Signal.open(
    bar.inst, want,
    stop_loss=f"{self.stop_atr}atr",
    take_profit=f"{self.target_atr}atr" if self.target_atr > 0 else None,
    trailing_stop=f"{self.trail_atr}atr" if self.trail_atr > 0 else None,
    reason=why,
)
if held:
    sig = sig.reversed()
```

5. 用 `Signal.open(inst, want, ...)` 一个写法同时覆盖多空；可选的止盈和追踪止损用 `... if ... else None` 关闭。
6. 有反向仓位时 `.reversed()`：一步反手，不受冷却限制。详见 [做空与反手](/help/strategy/short-reverse)。

最后按参数发通知。注意通知在**发出信号时**就发了，信号之后仍可能被风控拒绝，所以通知的意思是“策略想这么做”，成交与否以订单层的成交通知为准。

### on_signal_result 和 status

```python
def on_signal_result(self, ctx, result):
    if result.outcome not in ("ordered", "recorded", "pending"):
        ctx.log(f"信号 #{result.signal_id} {result.outcome}：{result.reason}", level="warn")
```

被拒、被跳过、过期的信号都记一条警告日志，排查时在卡片上就能看到。

`status()` 对每个品种输出一段：均线方向、快慢线差距、持仓，以及**在等什么**：`等下一次交叉`、`离快线太远，等回落`、`下一根收盘入场`、`不做多，空仓等待`。`cross` 方式为什么可能几周不开仓，见 [为什么没有信号](/help/strategy/no-signal) 里的案例。

## 币池残差反转 pool-reversal

在动态币池里找“相对大盘跌过头 / 涨过头”的币，等价格出现衰竭证据再反向入场。五层结构的整体思路见 [多币种与组合](/help/strategy/portfolio)，这里看代码细节。

### 模块级常量

```python
RULE = "resid-reversal"
BTC = "BTC-USDT-SWAP"
WATCH = Watchlist("watch")  # 观察名单存在 ctx.state["watch"]，重启不丢
```

`Watchlist` 只是一个“操作 `ctx.state` 的工具”，本身不存数据，所以放在模块级没问题。

### 声明

```python
universe = Screener(rule=RULE, side="both", top=10, refresh="1H")
timeframe = Timeframe("15m", options=["5m", "15m", "1H"], group="3 触发")
warmup = 60
```

每小时按评分（多空较强一侧）取前 10 个币作为标的，每根 15m K 线收盘时一起送来。参数的 `group` 用“1 筛选”“2 观察”……编号，表单里正好按五层排列。

### on_bars：五层串起来

```python
out = self._exits(ctx)                                        # 第 5 层：评分回归离场
cands = ctx.rank(RULE, side=self.direction, min_score=self.min_score)  # 第 1 层
WATCH.sync(ctx, cands, ttl_ms=int(self.setup_hours * 3600_000))  # 第 2 层
veto = self._btc_veto(ctx)
ready = []
for c in WATCH.items(ctx):                                    # 第 3 层
    if c.inst_id not in bars or ctx.position(c.inst_id) is not None or not self.allows(c.side):
        continue
    hits = self._confirmations(ctx, c.inst_id, c.side)
    WATCH.update(ctx, c.inst_id, hits=hits)
    if c.side in veto:
        continue
    if len(hits) >= min(self.need, self._enabled()):
        c["hits"] = hits
        ready.append(c)
```

- `direction` 参数直接当作 `ctx.rank` 的 `side` 用：它的选项 `both / long / short` 正好和 `side` 一致。
- `c.inst_id not in bars`：名单来自全池排名，但只有这次有 K 线送来的（即在 universe 前 10 里的）币才检查。
- 已有仓位、方向被禁用的跳过。
- 先把命中的确认项记进名单（`status()` 要显示），再看 BTC 否决。
- `min(self.need, self._enabled())`：如果只勾了 2 项却要求 3 项，就按 2 项算。四项全关时永远不会入场。

```python
for c in pick(ready, ctx.positions(), self.max_pos, self.max_side):  # 第 4 层
    out.append(Signal.open(c.inst_id, c.side, stop_loss=f"{self.stop_atr}atr",
                           trailing_stop=f"{self.trail_atr}atr" if self.trail_atr > 0 else None,
                           time_limit=f"{self.hold_hours}h", reason=reason))
    WATCH.remove(ctx, c.inst_id)
return out
```

按评分分配名额，入场后从名单移除。`time_limit` 让订单层到点平仓，策略不需要自己计时。

### 四项确认

```python
b = ctx.bars(inst, n=60)
if len(b) >= 30:
    if self.use_reclaim:
        e9 = ta.ema(b.c, 9)
        x = ta.crossover(b.c, e9) if long else ta.crossunder(b.c, e9)
        if x[-2:].any():
            hits.append("收复EMA9" if long else "跌破EMA9")
```

`x[-2:].any()` 的意思是“最近 2 根里发生过”，比只看最后一根宽松一点。RSI 拐头同理（`crossover(r, 30)` / `crossunder(r, 70)`）。

```python
d = ctx.series(inst, "delta", n=4)  # 主动买入 − 主动卖出（USDT），按本周期分桶
if len(d) >= 3 and math.isfinite(d.v[-1]):
    turned = (d.v[-1] > 0 and min(d.v[-3:-1]) < 0) if long else (d.v[-1] < 0 and max(d.v[-3:-1]) > 0)

oi = ctx.series(inst, "oi", tf="1H", n=5)
if len(oi) >= 4 and oi.v[0] > 0 and oi.v[-1] / oi.v[0] - 1 <= -0.005:
    hits.append("OI下降")
```

序列可能比要的短（数据缺口），所以每处都先判断长度。

### BTC 否决和评分离场

```python
b = ctx.bars(BTC, "1H", 30)
atr = ta.atr(b.h, b.l, b.c, 14)[-1]
move = b.c[-1] - b.c[-2]
if move < -self.btc_veto_atr * atr:
    return {"long"}        # BTC 最近 1 小时急跌：不做多
```

```python
for p in ctx.positions():
    long_s, short_s = ctx.score(RULE, p.inst_id)
    s = long_s if p.pos_side == "long" else short_s
    if math.isfinite(s) and s < self.exit_score:
        out.append(Signal.close(p.inst_id, reason=...))
```

持仓方向的评分掉到 `exit_score` 以下，说明“过头”已经修复，主动离场。那一侧没有评分时 `ctx.score` 返回 NaN，`math.isfinite(s)` 把这种情况排除掉：没分不等于分数低，不能当作离场理由。遍历的是 `ctx.positions()` 而不是本轮的 `bars`，所以已经掉出 universe 的持仓也会被检查。

### status

```
观察 SOL多91（1/2）、DOGE空86（1/2）、ARB多84（0/2）、OP空82（0/2） 等 6 个；持仓 1/4
```

最多列出评分最高的 4 个候选，每个显示方向、分数和“已满足 / 需要”的确认项数，一眼看出谁快要触发。

### 回测提示

默认周期 15m，回测要用 1m 数据，通常只能回测最近几天到几周；把周期改成 1H 可以回测更久（同时需要回测区间内有评分快照）。见 [试运行与回测](/help/strategy/backtest-trial)。
