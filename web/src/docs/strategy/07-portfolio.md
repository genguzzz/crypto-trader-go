---
title: 多币种与组合
summary: 动态标的池 Screener、一次处理所有品种的 on_bars、ctx.rank、Watchlist 和 pick；用币池残差反转讲解五层结构，以及为什么不能只凭评分入场。
---

# 多币种与组合

只交易 BTC、ETH 时，`on_bar` 逐个品种处理就够了。当你想“在几十个币里挑最好的几个”时，需要三样东西：

1. **动态标的池**：让系统按评分把候选送进来；
2. **一次看到所有品种**：才能在品种之间比较；
3. **名额分配**：总共最多持几个、多空各几个。

## Screener：动态标的池

```python
from ctg import Screener

universe = Screener(rule="resid-reversal", side="both", top=10, refresh="1H")
```

| 参数 | 默认 | 说明 |
|---|---|---|
| `rule` | `""` | 评分规则 id（扫描页的规则）。为空时按监控池排名取 |
| `side` | `"long"` | `long` 按做多分数排，`short` 按做空分数排，`both` 按两侧中更强的一侧排 |
| `top` | `20` | 取前几名，1–50 |
| `min_score` | 无 | 分数下限 |
| `filter` | 无 | 按因子范围过滤，如 `{"spread_bps": (0, 5)}` |
| `refresh` | `"1H"` | 多久重新筛选一次：`5m` `15m` `1H` `4H` `1D` |

工作方式：

- 只从系统的**活跃监控池**里选。
- 每到 `refresh` 的整点重新筛选一次，有变化就调用 `on_universe_change(ctx, added, removed)`，`ctx.universe` 是最新列表。
- 被移出池的合约不再送 K 线，但**已有持仓照常由订单层管理**（止损止盈、到期都有效）；策略想主动平它，要在别的事件里处理（比如遍历 `ctx.positions()`）。
- 创建实例时可以改成固定列表，或调整筛选参数。

## on_bars：一次拿到所有品种

```python
def on_bars(self, ctx, bars):
    # bars = {"BTC-USDT-SWAP": Bar, "SOL-USDT-SWAP": Bar, ...}，同一时刻收盘的全部品种
    out = []
    ...
    return out          # 返回信号列表
```

重写 `on_bars` 后，每个 K 线时刻只调用一次，`on_bar` 不再被调用。这样才能做排序、比较、分配名额。注意：在 `on_bars` 里任何一个品种的计算出错，整个事件都算出错，所以对单个品种的计算要做好数据不足的判断。

## ctx.rank：给全池排序

```python
cands = ctx.rank("resid-reversal", side="both", min_score=80)
for c in cands:
    c.inst_id, c.side, c.score      # "SOL-USDT-SWAP", "long", 91.0
```

`ctx.rank` 看的是**全池**，不只是 `universe` 里的品种；用 `among=[...]` 可以限制范围。详见 [数据接口 ctx](/help/strategy/ctx)。

## Watchlist：观察名单

候选不是“一入选就买”，而是先放进观察名单，等触发条件出现。`Watchlist` 把名单存在 `ctx.state` 里，重启不丢。

```python
from ctg import Watchlist

WATCH = Watchlist("watch")     # 存在 ctx.state["watch"]

WATCH.sync(ctx, cands, ttl_ms=4 * 3600_000)   # 和本轮候选对齐
for c in WATCH.items(ctx):                      # 分数从高到低
    c.inst_id, c.side, c.score, c.since         # since = 进入名单的时间（毫秒）
WATCH.update(ctx, "SOL-USDT-SWAP", hits=["RSI拐头"])   # 附加自定义字段
WATCH.remove(ctx, "SOL-USDT-SWAP")
WATCH.ids(ctx)                                   # 名单里的合约集合
```

`sync(ctx, candidates, ttl_ms)` 的规则：

- 新候选进入名单，`since = ctx.now`；
- 已在名单里的更新分数和方向；**方向变了就重新计时**；
- 不在本轮候选里的，或在名单里超过 `ttl_ms` 的，移出；
- 返回本轮被移出的合约列表。

## pick：分配名额

```python
from ctg import pick

chosen = pick(ready, ctx.positions(), max_total=4, max_side=3)
```

从 `ready`（每行要有 `inst_id side score`）里按分数从高到低挑：已经持有的跳过；已有持仓 + 新挑的不超过 `max_total`；每一侧不超过 `max_side`。

注意 `ctx.positions()` 只算本实例的持仓。账户还有自己的“最大持仓数”（默认 5，整个账户所有实例一起算），超了会被拒绝：`持仓数已达上限 5`。

## 案例：币池残差反转的五层

内置策略 `pool-reversal` 是一个完整的组合策略，它的 `on_bars` 分五层：

```python
def on_bars(self, ctx, bars):
    out = self._exits(ctx)                                        # 第 5 层：评分回归离场
    cands = ctx.rank(RULE, side=self.direction, min_score=self.min_score)  # 第 1 层
    WATCH.sync(ctx, cands, ttl_ms=int(self.setup_hours * 3600_000))  # 第 2 层
    ...
    for c in WATCH.items(ctx):                                    # 第 3 层：逐个检查触发
        ...
    for c in pick(ready, ctx.positions(), self.max_pos, self.max_side):  # 第 4 层
        out.append(Signal.open(c.inst_id, c.side, stop_loss=..., time_limit=f"{self.hold_hours}h", ...))
    return out
```

| 层 | 做什么 | 关键参数 |
|---|---|---|
| 1 评分筛选 | 全池按 `resid-reversal` 评分取候选（多空两侧） | `min_score`（默认 80，约前 20%） |
| 2 观察名单 | 候选入名单并计时；超时或掉出候选就移出 | `setup_hours`（默认 4 小时） |
| 3 触发确认 | 在本周期 K 线上找“跌势衰竭 / 涨势衰竭”的证据，满足至少 `need` 项才入场；BTC 急动时否决 | `need` 和四个开关、`btc_veto_atr` |
| 4 组合约束 | 已触发的按评分排序，受总数和单侧数量限制 | `max_pos`、`max_side` |
| 5 出场 | 持仓时限到期、评分回落、ATR 止损、可选追踪止损 | `hold_hours`、`exit_score`、`stop_atr`、`trail_atr` |

第 3 层的四项确认（做空方向是镜像）：

- **价格收复 EMA9**：最近 2 根内收盘上穿 9 周期 EMA；
- **RSI 拐头**：RSI(14) 最近 2 根内从 30 下方回到上方；
- **主动买卖转向**：最近一根主动买入净额（`ctx.series(inst, "delta")`）转正，之前两根里有负的；
- **OI 下降**：最近 4 小时持仓量下降至少 0.5%，说明被迫离场的杠杆已经出清。

逐行讲解见 [内置策略讲解](/help/strategy/builtins)。

## 为什么不能只凭评分入场

评分回答的是“哪些币相对大盘跌 / 涨得最过头”，**不回答“现在该不该买”**：

- 跌得最多的币，往往是因为有坏消息、有人在持续出货。评分高只说明它“极端”，不说明它“到底了”。直接买就是接飞刀：评分越高，可能跌得越凶。
- 评分是池内百分位排名，大盘整体暴跌时，排第一的币也可能还在加速下跌。
- 评分没有经过验证，只是排序依据。

所以评分只用来**缩小范围**（从几百个币到十几个），入场必须等**价格本身**给出证据：止跌、收复均线、主动买盘回来、杠杆出清。这就是第 2、3 层存在的原因。离场也一样：评分回落说明“极端”已经修复，是离场的理由；但保护本金靠的是止损，不是评分。

如果你写自己的组合策略，建议沿用这个结构：**排序挑候选 → 观察 → 价格触发 → 分配名额 → 有明确的出场**。

下一篇：[发通知](/help/strategy/notify)。
