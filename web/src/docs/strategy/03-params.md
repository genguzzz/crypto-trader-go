---
title: 参数声明
summary: 用 Int、Float、Bool、Choice、Timeframe 声明参数，实例表单自动生成；方向参数和 allows()；AI 可调。
---

# 参数声明

参数写成类属性，实例表单会根据声明自动生成输入控件。在处理函数里用 `self.<名字>` 读取，读到的就是这个实例填的值（已经转换成对应类型）。

```python
from ctg import Bool, Choice, Float, Int, Strategy, Timeframe


class Demo(Strategy):
    timeframe = Timeframe("4H", options=["1H", "4H", "1D"], group="信号")
    fast = Int(20, min=5, max=100, group="信号", label="快线长度", desc="快速 EMA 的 K 线根数")
    stop_atr = Float(2.0, min=0.5, max=6, step=0.25, group="风控", label="止损（ATR）")
    use_rsi = Bool(True, group="信号", label="RSI 过滤")
    direction = Choice("both", ["both", "long", "short"], labels=["多空双向", "只做多", "只做空"],
                       group="风控", label="方向")

    def on_bar(self, ctx, bar):
        n = self.fast * 3          # int
        k = self.stop_atr          # float
        if self.use_rsi: ...       # True / False
        if self.direction == "long": ...   # 字符串
```

## 参数类型

| 类型 | 写法 | 读到的值 | 表单控件 |
|---|---|---|---|
| `Int` | `Int(20, min=5, max=100)`，`step` 默认 1 | `int` | 数字输入框 |
| `Float` | `Float(2.0, min=0.5, max=6, step=0.25)` | `float` | 数字输入框 |
| `Bool` | `Bool(True)` | `True` / `False` | 开关 |
| `Choice` | `Choice("both", ["both", "long", "short"], labels=[...])` | 选中的字符串 | 选项不超过 4 个是按钮组，否则下拉框 |
| `Timeframe` | `Timeframe("4H", options=["1H", "4H", "1D"])` | 周期字符串 | 同 Choice，默认标签“K 线周期” |
| `Param` | `Param(20, min=5, max=100)` | 默认值是整数就是 `int`，否则 `float` | 数字输入框（老写法，仍然可用） |

所有类型都可以带这几个共同参数：

| 参数 | 作用 |
|---|---|
| `group` | 表单里的分组标题。按参数第一次出现的顺序排列；只有一个分组时不显示标题 |
| `label` | 显示名称。不写就显示变量名；写了会同时显示名称和变量名 |
| `desc` | 说明文字，显示在名称下面，后面自动跟上“默认 … ，范围 …” |
| `ai_tunable` | 标记“AI 可调”，见下文 |

数字类型另有 `min`、`max`、`step`。`Choice` 另有 `options`（选项，必填）和 `labels`（每个选项的显示名，可选，数量要和 `options` 一致）。

表单上每个参数旁边有“恢复默认”按钮；超出范围的值会标红。

## 检查会拦下的声明问题

- 默认值不在 `[min, max]` 里，或 `min > max`
- `Int` 的默认值不是整数
- `Choice` 的 `options` 为空、有重复，或默认值不在选项里
- `labels` 和 `options` 数量不一致
- `Timeframe` 的选项不是 `1m 5m 15m 1H 4H 1D` 之一
- 把 `timeframe` 写成了 `Int` / `Choice` 等其他参数（要用 `Timeframe`）

## 值在系统里怎么存

每个参数的值都存成**一个数字**：

| 类型 | 存的数字 | 例子 |
|---|---|---|
| `Int` / `Float` | 数值本身 | `20`、`2.5` |
| `Bool` | `1` 或 `0` | `True` → `1` |
| `Choice` / `Timeframe` | 选项的**序号**（从 0 开始） | `["both", "long", "short"]` 里的 `"long"` → `1` |

运行时 SDK 会自动转换回来，你在代码里看到的永远是 `int`、`float`、`bool` 或字符串。传进来的值还会被夹到 `[min, max]` 范围内，`Int` 会四舍五入。

> 因为存的是序号，在实例配置、AI 调参建议等原始数据里看到的 Choice 值是数字（比如 `direction: 1`），对照 `options` 的顺序就知道是哪个选项。同理，新版本里给 Choice 加选项时最好加在末尾，不要打乱已有顺序，免得你对照新旧配置时看错。

`ctx.params` 是所有参数当前值的字典（已转换类型），方便一次性打印：`ctx.log(str(ctx.params))`。

## 让实例自己选周期

```python
timeframe = Timeframe("4H", options=["1H", "4H", "1D"], group="信号")
```

这样每个实例都能选自己的周期：同一个策略可以一个实例跑 1H、另一个跑 4H。实例选了什么，K 线就按什么周期送来，`ctx.timeframe` 和 `self.timeframe` 都是这个值，`ctx.bars(inst)` 不写周期时也用它。止损写 `"2atr"` 时的 ATR 同样按这个周期算。

`options` 只列你测试过的周期。周期越短，回测需要的 1 分钟数据越多（见 [试运行与回测](/help/strategy/backtest-trial)）。

## 方向参数和 allows()

很多策略想让用户选“只做多 / 只做空 / 双向”。约定是声明一个名叫 `direction` 的 `Choice`，选项用 `both`、`long`、`short`：

```python
direction = Choice("both", ["both", "long", "short"], labels=["多空双向", "只做多", "只做空"],
                   group="风控", label="方向")
```

然后在开仓前调用 `self.allows(side)`：

```python
if not self.allows(want):          # want 是 "long" 或 "short"
    return None
```

`allows(side)` 同时看两样东西：

1. 类属性 `can_long` / `can_short`：类上写了 `can_short = False`，`allows("short")` 永远是 False；
2. 有 `direction` 参数时：它是 `both` 或正好等于 `side` 才返回 True。

`side` 只能是 `"long"` 或 `"short"`，写错会抛 `ValueError`。

两者的区别：`can_long` / `can_short` 订单层也会检查，策略声明不做的方向发出开仓信号会被拒绝；`direction` 参数只有你在代码里调用 `allows()` 才生效，订单层并不知道它。所以“只做多”的实例要靠 `allows()` 才不会开空。详见 [做空与反手](/help/strategy/short-reverse)。

## AI 可调（ai_tunable）

```python
fast = Int(20, min=5, max=100, ai_tunable=True)
```

标了 `ai_tunable=True` 的参数在表单上有“AI 可调”标签。AI 层复盘时可以对这些参数**提出**调整建议，但只有你批准后才会生效，不会自动改。没标的参数 AI 不会碰。适合标注的是“调了不改变策略本质”的数值，比如均线长度、止损倍数；方向、开关一类最好不标。

## 小建议

- 参数的 `label` 和 `desc` 写中文、写清单位（“止损（ATR）”、“持仓时限（小时）”），一个月后的你会感谢现在的你。
- 参数之间有约束（比如快线必须小于慢线）时，表单拦不住，要在代码里判断，并在 `status()` 里提示，参考 [内置策略讲解](/help/strategy/builtins) 里的大币均线趋势。

下一篇：[数据接口 ctx](/help/strategy/ctx)。
