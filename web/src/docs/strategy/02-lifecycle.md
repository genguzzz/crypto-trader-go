---
title: 策略结构与生命周期
summary: 类属性、每个处理函数在实时和回测里什么时候被调用、同一时刻的事件顺序，以及出错和超时会怎样。
---

# 策略结构与生命周期

一个策略是 `Strategy` 的子类：用**类属性**描述自己，用**处理函数**（handler）响应事件。所有处理函数都是可选的，不写就什么都不做。

## 类属性

| 属性 | 默认值 | 说明 |
|---|---|---|
| `name` | 类名转小写加 `-`（`MyBreakout` → `my-breakout`） | 唯一标识，2–40 个小写字母、数字或 `-` |
| `description` | 类的 docstring | 一句话说明，显示在策略卡片上 |
| `universe` | `[]` | 交易的合约：固定列表 `["BTC-USDT-SWAP", …]`（最多 50 个），或 `Screener(...)` 动态池，见 [多币种与组合](/help/strategy/portfolio) |
| `timeframe` | `"1H"` | K 线周期：`1m` `5m` `15m` `1H` `4H` `1D`；写成 `Timeframe("4H")` 可让每个实例自己选，见 [参数声明](/help/strategy/params) |
| `can_long` / `can_short` | `True` / `True` | 策略可能开的方向，显示在卡片上，订单层会据此拦截；配合 `self.allows()` 使用 |
| `warmup` | `0` | 策略要读多少根历史 K 线，只是给人看的说明，不会自动等待 |
| `schedule` | `{}` | 定时器：`{"check": "15m"}` 表示每 15 分钟调用一次 `on_timer(ctx, "check")` |

> `can_long` / `can_short` 会被订单层执行：声明了 `can_short = False` 的策略发出开空信号，会被拒绝（`策略声明了不做空（can_long / can_short）`）。两个都写 False 的策略（比如只发通知的）不受限制。代码里先用 `self.allows("short")` 判断，就不会发出注定被拒的信号，见 [做空与反手](/help/strategy/short-reverse)。

## 处理函数

每个处理函数都可以返回一个 `Signal`、一个 `Signal` 列表，或 `None`。

| 处理函数 | 什么时候调用 |
|---|---|
| `on_start(ctx)` | 进程启动或重启后一次。实时运行时 `ctx.state` 已恢复；回测里 `ctx.state` 从空字典开始 |
| `on_universe_change(ctx, added, removed)` | 标的池变化时。**每次启动后也会调用一次**，`added` 是全部标的、`removed` 为空 |
| `on_bar(ctx, bar)` | 每个品种每根收盘的 K 线一次（`bar.inst bar.ts bar.o bar.h bar.l bar.c bar.vol bar.volq`） |
| `on_bars(ctx, bars)` | 同一时刻收盘的所有品种一次，`bars = {品种: Bar}`。重写它之后 `on_bar` 不再被调用 |
| `on_timer(ctx, name)` | `schedule` 里的定时器到点 |
| `on_fill(ctx, fill)` | 本实例的一笔成交（开仓、平仓、止损、止盈、到期、强平都算） |
| `on_position_closed(ctx, trade)` | 一笔成交让本实例在该品种的持仓归零时，紧跟在 `on_fill` 之后。反手时平掉旧仓那一笔也会触发 |
| `on_signal_result(ctx, result)` | 信号有了去向：`result.signal_id` `result.outcome` `result.reason` |
| `on_funding(ctx, inst, rate, amount)` | 持仓结算资金费。`amount` 为负表示付出 |
| `on_stop(ctx)` | 实例停止、回测结束前 |
| `status(ctx)` | 不是事件。**每个事件处理完后**都会调用，返回的一行字（最多 300 字符）显示在实例卡片上 |

几个细节：

- `bar.ts` 是这根 K 线的**开盘**时间（毫秒）；收盘时间是 `bar.ts` 加一个周期。
- `fill` 的字段：`inst_id pos_side side qty px fee realized_pnl kind closed ts`。`closed` 为 True 表示这笔成交让持仓归零（之后会调用 `on_position_closed`）。`kind` 是 `fill`（信号下的单）、`stop`、`target`、`time`（持仓到期）、`liquidation`。
- `trade`（`on_position_closed`）的字段：`inst_id pos_side exit_px pnl kind ts`。`pnl` 是这笔平仓成交的已实现盈亏减去它的手续费，不含开仓手续费和资金费。
- 不重写 `on_bars` 时，同一时刻的品种按合约名排序，逐个调用 `on_bar`；某个品种出错不影响其他品种。

## 什么时候收到 K 线

K 线周期按 **UTC 时间**对齐：`1H` 在每个整点，`4H` 在 UTC 0、4、8、12、16、20 点，`1D` 在 UTC 0 点（北京时间 8 点）收盘。

- **实时**：行情系统确认某一分钟的数据后（通常收盘后几秒），检查哪些实例的周期在这一分钟收盘，把收盘的 K 线发过去。某个品种这一根缺数据时，这一轮不会包含它。
- **回测**：按基础周期一根一根回放（有任何周期或定时器小于 1H 时用 1m，否则用 1H），在每个周期收盘的时刻发出 K 线，和实时完全一样的代码路径。

## 同一时刻的事件顺序

**实时**，每分钟 T（某根 K 线的收盘时刻）：

1. 账户结算：资金费（→ `on_funding`）、日亏损熔断检查、到期持仓平仓（→ `on_fill`、`on_position_closed`）。
2. 对每个运行中（未暂停）的实例：
   1. 动态池到了刷新时间就重新筛选，有变化 → `on_universe_change`
   2. T 是实例周期的整数倍 → K 线批次 → `on_bars`，或逐个 `on_bar`
   3. 到点的定时器（按名字排序）→ `on_timer`
3. 随时发生：止损、止盈、追踪止损、强平的成交（订单层大约每 1.5 秒看一次实时价格），以及信号处理完的结果。它们按发生顺序排队送给策略。

**回测**，每根基础 K 线 [t, T)：

1. 上一步留下的市价单在这根 K 线**开盘价**成交；止损止盈按这根的最高、最低价判断。
2. 时钟走到 T：资金费、熔断、到期检查。
3. 依次送出：排队的成交和信号结果 → 标的池变化 → K 线 → 定时器。

一个实例的事件**严格一个接一个**处理（上一个处理完才发下一个），所以处理函数里不用担心并发。

## 每个事件之后

- `ctx.state` 若有变化就保存（JSON 字典，最大 64 KB，超过不保存并写一条错误日志）。实时运行时重启后恢复；回测每次从空字典开始。
- 调用 `status(ctx)`，文字变了就更新卡片。`status()` 抛异常一次后就不再调用（直到重启），所以写它要小心。

## 出错、超时和暂停

| 情况 | 结果 |
|---|---|
| 处理函数抛异常 | 写进运行日志（卡片上能看到），事件照常确认，进程继续运行 |
| **连续 3 个事件**都抛异常 | 实时：实例**暂停**，原因写在卡片上，修好后点继续；回测：回测失败 |
| 一个事件超过 **5 秒**没处理完（K 线批次每多一个品种多给 0.5 秒），或进程崩溃 | 进程被杀，实例标为故障，2 / 4 / 6 秒后用保存的 `ctx.state` 重启，最多重启 3 次，再故障就停止。正常运行满 10 分钟后才出的故障，重新算作第 1 次 |
| 暂停中 | 不再收到 K 线和定时器；成交和信号结果照常送达；此时发出的信号会被拒绝（`实例未运行`） |

## 为什么没有“每秒回调”

回调都以 K 线收盘为准，最短 1 分钟，这是有意的设计：

- 历史数据只有 1m 和 1H 两种精度。每秒触发的策略**无法回测**，你没法知道它到底好不好。
- 真正需要盯盘的部分——止损、止盈、追踪止损、持仓到期——由订单层用实时价格执行，不需要策略每秒醒来。你只要在信号里写好 `stop_loss`、`trailing_stop`、`time_limit`。

## 和其他框架比

| 框架 | 模型 | 这里对应什么 |
|---|---|---|
| TradingView Pine | 每根 K 线把整个脚本重跑一遍 | `on_bar` + `ta` 指标（名字和算法都照 Pine） |
| Freqtrade | 一次算出整列买卖信号（向量化） | 在 `on_bar` 里对 `ctx.bars()` 的 numpy 数组做向量计算，只看最后一个值 |
| NautilusTrader、Backtrader、Jesse | 事件驱动：K 线、成交、定时器各有回调 | `on_bar` `on_fill` `on_timer` 等，本 SDK 就是这一类 |
| QuantConnect LEAN | 流水线：选股 → 信号 → 组合 → 风控 → 执行 | `Screener` / `ctx.rank` → 你的判断 → `pick` → 账户风控 → 订单层，见 [多币种与组合](/help/strategy/portfolio) |

下一篇：[参数声明](/help/strategy/params)。
