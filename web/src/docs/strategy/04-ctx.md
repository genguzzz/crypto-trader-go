---
title: 数据接口 ctx
summary: ctx 的每个成员：K 线、数据序列、因子、评分和筛选、持仓与账户、状态、日志与通知。
---

# 数据接口 ctx

每个处理函数的第一个参数都是 `ctx`。它是策略读数据、发信号、记日志的唯一入口。所有读取都是**截至当前事件时刻**的：回测里不会读到“未来”的数据。

本篇按用途讲解；逐个字段的速查见 [API 参考](/help/strategy/api)。

同一个事件里，用相同参数调用 `ctx.bars` / `ctx.series` 只会真正查询一次（有缓存），所以在 `on_bar` 和 `status()` 里重复调用不必担心性能。

## 一览

| 成员 | 返回 |
|---|---|
| `ctx.bars(inst, tf=None, n=200)` | `Bars`：已收盘 K 线的 numpy 数组 |
| `ctx.series(inst, field, tf=None, n=100)` | `Series`：持仓量、资金费率、主动买卖额 |
| `ctx.factors(inst)` | `dict`：事实面板的因子值 |
| `ctx.rank(rule, side="long", among=None, min_score=None, top=None)` | 按评分排序的行列表 |
| `ctx.screener(rule="", side="long", top=20, min_score=None, filter=None)` | 筛选器原始结果 |
| `ctx.score(rule, inst)` | `(long, short)` 两个分数 |
| `ctx.position(inst)` / `ctx.positions()` | 本实例的持仓 |
| `ctx.account()` | 账户概况 |
| `ctx.spec(inst)` | 合约规格 |
| `ctx.state` | 持久化的字典 |
| `ctx.submit(signal)` | 立刻发出信号，返回信号 id |
| `ctx.log(msg, level="info")` | 写运行日志 |
| `ctx.notify(text, level="info", **data)` | 发消息到 Telegram / Webhook |
| `ctx.now` `ctx.mode` `ctx.params` `ctx.universe` `ctx.timeframe` `ctx.instance_id` | 当前时间、运行模式、参数值、当前标的、周期、实例编号 |

## ctx.bars：K 线

```python
b = ctx.bars("BTC-USDT-SWAP")              # 实例周期，最近 200 根
b = ctx.bars("BTC-USDT-SWAP", "1H", 30)    # 指定周期和根数
len(b)                                      # 实际拿到几根（可能少于 n）
b.c[-1]                                     # 最新一根已收盘 K 线的收盘价
```

`Bars` 有 7 个 numpy 数组，**最旧的在前、最新的在最后**：

| 字段 | 含义 |
|---|---|
| `b.ts` | 开盘时间，毫秒（int64） |
| `b.o` `b.h` `b.l` `b.c` | 开、高、低、收 |
| `b.vol` | 成交量 |
| `b.volq` | 成交额（USDT） |

要点：

- **只返回已收盘的 K 线**。在 `on_bar` 里，`b` 的最后一根就是刚送来的 `bar`。
- `n` 最多 1000。
- 历史不够时返回的根数会少于 `n`，所以**一定要先判断 `len(b)`**。
- 缺数据的那一根直接不出现（数组里不会有 NaN 占位）。对时间敏感的计算可以检查 `b.ts` 是否连续。
- 周期可以和实例周期不同（比如 15m 策略读 1H 的 BTC 判断大盘，或 4H 策略读 15m 找入场点）。回测里比基础周期更细的 K 线由 1m 历史合成，只能读到 1m 数据保留的范围内，更早的部分会少根，见 [试运行与回测](/help/strategy/backtest-trial)。

配合 `ta` 指标使用：

```python
from ctg import ta

e20 = ta.ema(b.c, 20)
atr = ta.atr(b.h, b.l, b.c, 14)
if ta.crossover(e20, ta.ema(b.c, 50))[-1]:
    ...
```

`ta` 提供 `sma ema rma rsi atr tr macd bbands(bb) crossover crossunder cross highest lowest stdev change`，名字和算法照 TradingView Pine。输入数组，返回同样长度的数组；预热期和遇到缺口时是 NaN，不会抛异常。`bbands` 返回 `(中轨, 上轨, 下轨)`，`macd` 返回 `(macd, signal, 柱)`。

## ctx.series：数据序列

```python
oi = ctx.series(inst, "oi", tf="1H", n=5)
oi.v[-1] / oi.v[0] - 1        # 最近 4 小时持仓量变化
```

`Series` 有 `ts`（时间桶的开始时间，毫秒）和 `v`（数值）两个数组，最旧在前。只包含**完整的**时间桶。

| field | 含义 | 单位 |
|---|---|---|
| `oi` | 持仓量，取每个桶内最后一次采样（即桶收盘时） | USDT |
| `funding` | 已结算的资金费率，每次结算一个点；`tf` 不起作用，`n` 是结算次数 | 比例（0.0001 = 0.01%） |
| `buy` | 主动买入成交额 | USDT |
| `sell` | 主动卖出成交额 | USDT |
| `delta` | `buy − sell`，正数表示主动买盘占优 | USDT |

`buy` / `sell` / `delta` 优先用本系统记录的逐笔成交；没有记录的时段用交易所的主动买卖量历史补齐（5 分钟或 1 小时精度，所以 1m 周期只有记录过的分钟）。覆盖不完整的桶会被跳过，因此 `len()` 可能小于 `n`，`ts` 也可能不连续。`n` 最多 1000。

## ctx.factors：因子

```python
f = ctx.factors("ETH-USDT-SWAP")
f.get("ret_4h"), f.get("spread_bps"), f.get("funding_hourly")
```

返回扫描页事实面板上最近一个已确认分钟的因子值，缺失的是 NaN；未知合约返回 `{}`。回测里只有可以从历史重建的因子（按最近一个整点计算）。

## 评分：rank、screener、score

系统的评分规则（扫描页里的规则，比如 `resid-reversal`）给监控池里每个合约打多空两个分数（0–100）。三个接口的区别：

| 接口 | 用途 | 返回 |
|---|---|---|
| `ctx.rank(rule, side, among, min_score, top)` | **挑候选**：全池按分数排序 | 行列表，每行 `inst_id side score long short values`，分数从高到低 |
| `ctx.screener(rule, side, top, min_score, filter)` | 筛选器原始结果，可按因子范围过滤 | 行列表，每行 `inst_id rank last long short values` |
| `ctx.score(rule, inst)` | **查一个合约**的当前分数 | `(long, short)` |

```python
# 全池做多分数 ≥ 80 的前 5 个
for r in ctx.rank("resid-reversal", side="long", min_score=80, top=5):
    ctx.log(f"{r.inst_id} {r.score:.0f}")

# side="both"：每行取多空中更强的一侧，r.side 告诉你是哪侧
cands = ctx.rank("resid-reversal", side="both", min_score=80)

# 只在指定合约里排
ctx.rank("resid-reversal", among=["BTC-USDT-SWAP", "ETH-USDT-SWAP"])

# 价差 ≤ 5bp 的前 20 个
ctx.screener("resid-reversal", side="long", top=20, filter={"spread_bps": (0, 5)})

long_s, short_s = ctx.score("resid-reversal", "SOL-USDT-SWAP")
```

注意：

- 分数是**在池内的百分位排名**，不是胜率，也没有经过验证。它只回答“谁最突出”，不回答“现在该不该买”。为什么不能只凭分数入场，见 [多币种与组合](/help/strategy/portfolio)。
- 规则带成本门槛（比如价差太大）时，被门槛挡掉的合约不会出现在结果里。
- `ctx.score` 里没有评分的一侧是 NaN（合约完全没评分时两个都是 NaN），实时和回测一样。用 `math.isfinite()` 判断后再比较。`ctx.rank` / `ctx.screener` 的行里，没评分的一侧显示为 `0`。
- 回测里用的是整点的历史评分快照，规则在回测区间内没有快照会报错。

## 持仓、账户、合约

```python
pos = ctx.position("BTC-USDT-SWAP")       # 本实例在该合约的持仓，没有则 None
if pos and pos.pos_side == "long":
    ctx.log(f"持多 {pos.qty} 张，均价 {pos.entry_px}，浮盈 {pos.unrealized_pnl:.2f}")
```

持仓字段：`inst_id pos_side qty`（张）`ct_val`（每张多少币）`entry_px leverage margin liq_px mark_px unrealized_pnl stop_loss take_profit opened_at`（毫秒）。

- `ctx.positions()`：本实例全部持仓的列表。**只包含本实例的**，同账户其他实例的持仓看不到。
- `ctx.account()`：`equity`（权益）`cash`（可用）`day_pnl halted`（今天是否已熔断）`open_count`，以及 `limits`（账户规则，如 `limits.risk_pct`、`limits.max_positions`）。
- `ctx.spec(inst)`：`ct_val tick_sz lot_sz min_sz max_lever`，未知合约返回 None。

所有返回的记录既能用 `pos.entry_px` 也能用 `pos["entryPx"]` 读取。

## ctx.state：跨事件保存数据

```python
ctx.state["count"] = ctx.state.get("count", 0) + 1
```

普通字典，每个事件处理完自动保存，实时运行时重启后恢复。要求：

- 只能放 JSON 能表示的东西：数字、字符串、布尔、列表、字典（键用字符串）。numpy 数值先转 `float()`，NaN 不行。
- 序列化后不超过 64 KB，超过不保存并写错误日志。
- 回测每次从空字典开始。

处理函数之外的全局变量和 `self` 上的属性在重启后会丢失，需要保留的东西都放 `ctx.state`。

## ctx.submit、ctx.log、ctx.notify

- `ctx.submit(signal)`：立刻发出信号并返回信号 id；参数有误或被拒收时返回 None（并写一条警告日志）。一般直接 `return` 信号就行，需要拿到 id 时才用它。
- `ctx.log(msg, level="info")`：写进实例的运行日志，`level` 可以是 `info` `warn` `error`。卡片和试运行结果里都能看到，每个实例保留最近 300 条。
- `ctx.notify(text, level="info", **data)`：发消息给你，见 [发通知](/help/strategy/notify)。

## 其他属性

| 属性 | 含义 |
|---|---|
| `ctx.now` | 当前事件的时间（毫秒）。回测里是回放时钟，**不要用 `time.time()`** |
| `ctx.mode` | `"live"` 或 `"backtest"`（试运行也是 backtest） |
| `ctx.params` | 参数当前值的字典 |
| `ctx.universe` | 当前标的列表（动态池时随筛选变化） |
| `ctx.timeframe` | 实例的 K 线周期 |
| `ctx.instance_id` | 实例编号 |

下一篇：[信号、止损止盈与仓位计算](/help/strategy/signals)。
