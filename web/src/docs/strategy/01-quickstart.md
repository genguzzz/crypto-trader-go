---
title: 快速开始
summary: 5 分钟写出第一个策略，检查、试运行、发布，再挂到账户上运行。
---

# 快速开始

策略是一段 Python 代码。它**只负责发信号**（开多、开空、平仓、减仓），下单、风控、算仓位、止损止盈的执行都由账户（订单层）统一完成。所以写策略时你只需要回答一个问题：**这根 K 线收盘后，我想做什么？**

## 整体流程

1. 打开 **策略页**，点 **新建策略**。
2. **选模板**：单品种趋势、多币种评分组合、定时检查 + 发通知，或空白。从模板开始比从零写省事。内置策略是只读的，想改它就打开它点 **复制为新策略**，会得到一份名字后面带 `-copy` 的草稿。
3. **编辑**：在编辑器里改代码。输入停顿后草稿会自动保存（Ctrl/⌘+S 立即保存），不会影响任何正在运行的东西。
4. **检查**：保存时自动检查，也可以点 **检查**：语法、未定义的名字、参数声明是否合理、周期和品种写得对不对。红色是错误（必须改），黄色是提醒。
5. **试运行**：在“参数与试运行”里点 **试运行**，用最近 N 天（默认 14 天，最多 60 天）的历史数据跑一遍，几十秒内看到信号、成交、日志和图表。详见 [试运行与回测](/help/strategy/backtest-trial)。
6. **发布**：检查通过后发布，生成一个**不可变的版本**。之后再改代码只改草稿，已发布的版本和用它的实例都不受影响。
7. **在账户里添加实例**：到交易账户里添加策略实例，选刚发布的版本，填参数和交易标的，选执行方式（自动 / AI 审核 / 只记录），启动。

> 新策略建议先用 **只记录** 方式跑几天：信号照常产生和记录，但从不下单。

## 第一个策略：BTC 1 小时突破

下面是一个完整、能直接运行的策略。思路很简单：1 小时 K 线收盘价突破前 20 根的最高价就做多，止损 2 个 ATR，止盈 4 个 ATR。

```python
from ctg import Float, Int, Signal, Strategy, ta


class MyBreakout(Strategy):
    """BTC 1H breakout: go long when the close breaks the highest high of the previous N bars."""

    name = "my-breakout"                      # 唯一标识：小写字母、数字、- ，2–40 个字符
    description = "BTC 1 小时突破：收盘突破前 N 根最高价做多，ATR 止损止盈。"
    universe = ["BTC-USDT-SWAP"]              # 交易哪些合约
    timeframe = "1H"                          # 每根 1 小时 K 线收盘调用一次 on_bar
    can_short = False                         # 只做多（显示在策略卡片上）
    warmup = 60                               # 需要的历史 K 线根数（给人看的说明）

    lookback = Int(20, min=5, max=100, label="突破回看根数", desc="和前多少根 K 线的最高价比较")
    stop_atr = Float(2.0, min=0.5, max=6, step=0.25, label="止损（ATR）", desc="止盈是它的 2 倍")

    def on_bar(self, ctx, bar):
        if ctx.position(bar.inst) is not None:
            return None                           # 已有仓位，交给止损止盈处理
        b = ctx.bars(bar.inst, n=self.lookback + 40)
        if len(b) < self.lookback + 2:
            return None                           # 历史不够
        prev_high = ta.highest(b.h, self.lookback)[-2]   # 前 N 根（不含当前这根）的最高价
        if bar.c > prev_high:
            return Signal.open_long(
                bar.inst,
                stop_loss=f"{self.stop_atr}atr",
                take_profit=f"{2 * self.stop_atr}atr",
                reason=f"收盘 {bar.c:g} 突破前 {self.lookback} 根最高 {prev_high:g}",
            )
        return None

    def status(self, ctx):
        b = ctx.bars("BTC-USDT-SWAP", n=self.lookback + 40)
        if len(b) < self.lookback + 1:
            return "数据不足"
        high = ta.highest(b.h, self.lookback)[-1]
        return f"前 {self.lookback} 根最高 {high:g}，现价 {b.c[-1]:g}，等待突破"
```

逐段看：

- **类属性**（`name`、`universe`、`timeframe` 等）描述策略本身，见 [策略结构与生命周期](/help/strategy/lifecycle)。
- **参数**（`Int`、`Float`）会自动变成实例表单里的输入框，在代码里用 `self.lookback` 读取，见 [参数声明](/help/strategy/params)。
- **`on_bar(ctx, bar)`**：每个品种每根收盘的 K 线调用一次。`bar` 是刚收盘的那根；`ctx.bars()` 返回的最后一根就是它。
- **返回值**：一个 `Signal`、一组 `Signal`，或 `None`（不操作）。
- **止损写法** `"2.0atr"`：SDK 会换算成具体价格（参考价 − 2 × ATR(14)）。账户默认**要求开仓信号带止损**，不带会被拒绝。见 [信号、止损止盈与仓位计算](/help/strategy/signals)。
- **`status()`**：返回一行字，显示在实例卡片上，告诉你策略现在在等什么。强烈建议每个策略都写。

## 检查会报什么

类声明上的问题会带上 `类名.属性:` 前缀，比如 `MyBreakout.lookback: 默认值 5 超出范围 [10, None]`，并定位到那一行。

| 提示 | 级别 | 怎么改 |
|---|---|---|
| `语法错误：…` | 错误 | 看行号，通常是括号、冒号、缩进 |
| `undefined name 'xxx'` | 错误 | 变量或函数名拼错、忘了 import（这类提示来自 Python 检查工具，是英文） |
| `'xxx' imported but unused` 等 | 提醒 | 可以不管 |
| `NameError: …` 等 | 错误 | 代码在加载时就出错了（类体或模块顶层的代码），看行号 |
| `没有找到 Strategy 子类（…）` | 错误 | 文件里要有 `class Xxx(Strategy):` |
| `一个文件只能定义一个策略，找到了：…` | 错误 | 一个文件只写一个策略类 |
| `name '…' 需为 2–40 个小写字母、数字或 -` | 错误 | 改 `name` |
| `周期 '…' 不在 [...] 里` / `周期参数请用 Timeframe(...)` | 错误 | 周期只能是 `1m 5m 15m 1H 4H 1D`；做成参数要用 `Timeframe` |
| `'…' 不是 BTC-USDT-SWAP 这样的合约 ID` | 错误 | 合约要写全称 |
| `universe 为空：列出合约，或用 Screener` | 提醒 | 固定列表为空时，记得在实例里选标的或改用 Screener |
| `最多 50 个合约` / `Screener 的 top 需在 1–50` | 错误 | 固定列表和活跃池筛选最多 50 个；全市场筛选（`scope="market"`）最多 300 个 |
| `全市场 Screener 按成交额排序，不能指定 rule` | 错误 | `scope="market"` 不用评分，去掉 `rule` / `min_score` |
| `Screener 的 refresh 需为 5m / 15m / 1H / 4H / 1D` / `Screener 的 min_score 需在 0–100` | 错误 | 见 [多币种与组合](/help/strategy/portfolio) |
| `定时器 'x' 的间隔 '…' 不在 [...] 里` | 错误 | `schedule` 的间隔同样只能是上面 6 种周期 |
| `默认值 5 超出范围 [10, None]` 等参数问题 | 错误 | 默认值要在范围内，见 [参数声明](/help/strategy/params) |
| `… 没有重写 on_bar / on_bars / on_timer：它永远不会做任何事` | 提醒 | 至少写一个会被定时调用的处理函数 |

发布时还有两条规则：检查不能有错误；策略名不能和内置策略或你的另一个策略重名（复制来的策略记得改 `name`）。

## 运行环境

- 只能用 Python 标准库和 `numpy`，没有网络，不能读写文件。
- 每个实例一个独立进程，内存上限 256 MB。
- 每个事件要在 **5 秒** 内处理完，否则进程会被杀掉重启。别在 `on_bar` 里做重计算。

## 下一步

- 想知道 `on_bar` 什么时候被调用、还有哪些处理函数：[策略结构与生命周期](/help/strategy/lifecycle)
- 想读更多数据（持仓量、资金费率、评分）：[数据接口 ctx](/help/strategy/ctx)
- 没出信号、信号没成交：[为什么没有信号](/help/strategy/no-signal)
- 看看完整的例子：[内置策略讲解](/help/strategy/builtins)
- 查所有类、方法和字段：[API 参考](/help/strategy/api)
