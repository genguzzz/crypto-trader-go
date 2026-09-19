# 多空入场、仓位离场与量化验证：可计算而不冒充有效

> **研究规范，不是投资建议或已通过回测的策略。** 核心原则：**方向证据**、**执行后的净价值**、**风险许可**、**持仓继续持有价值**分别建模。高多头分不等于应该开多；低多头分不等于该开空、也不等于现有多仓应立刻止损。OI 同时有未平多/空双方，成交量也不能直接表示主动买卖方向。[CME OI 定义](https://www.cmegroup.com/education/lessons/open-interest)

## 1. 评分对象、时钟和输出

| 对象 | 只回答什么问题 | 不回答什么问题 |
|---|---|---|
| `entry_long` / `entry_short`（每币每模型版本） | 在预定研究时域内的方向性**证据强弱/候选优先级**，0–100 排名指数 | 不是上涨/下跌概率，不是订单指令，不因为排第一就有净收益 |
| `netEdgeLong` / `netEdgeShort`（**验证后再有**） | 假设买卖价、滑点、资金费和流动性后的预期净价值 | 没有可估概率/标签/完整成本前，**不输出数值** |
| `exit_long` / `exit_short`（真实持仓各自） | 对现有仓位继续持有、减仓/保护与平仓情景的风险和理由 | 不是 `100-entryLong/Short`；未知仓位/保证金不能给精确建议 |
| `gate/quality` | 交易可用性、数据和持仓风险许可 | 不能由很高的分数抵消硬风险。 |

最初固定评估时域（供用户选择）：例 `bar=1m` 作数据原子；趋势窗口 15m/1h/4h；每 5 秒 UI 可显示盘中 `preview`（会重绘），每完整 1m `confirmed`；**研究标签**暂以未来 4h 的净收益/路径作为预注册候选，未完成历史验证前不得声称“最佳进场”。`t_decide` 的任一特征必须 `availableAt ≤ t_decide`，而不是只看 K 线开盘时间；未来标签起点须为 `t_fill ≥ t_decide + 实测/保守延迟` 的可成交买卖盘。[OKX candle confirm](https://www.okx.com/docs-v5/en/#order-book-trading-market-data-get-candlesticks)

每个解释项至少：`factorId, rawValue, rawUnit, lookback, normalizedValue, weight, directionContribution, source, exchangeTs, receivedAt, quality, modelVersion`。输入总表见[运行契约](runtime-and-data-contracts.md)。

## 2. 逐因子定义、适用条件及“不能推断什么”

> 标准化仅使用**决策前**该币/流动性分组的滚动中位数、MAD 或分位数，截断异常值。预处理器和 lookback 随模型版本冻结；不把全样本未来统计量用于历史当时。

| 组 / 候选因子 | 原始字段与计算（都保存单位/窗口） | 可说明 / 不可说明 |
|---|---|---|
| 趋势 `T` | 同币永续 `log(C_t/C_t-k)` / 过去窗口 realized vol，15m/1h/4h 多周期冲突标识；仅确认柱形成 confirmed | 趋势持续的候选证据，不保证下一次继续；不能用 5s 预览回测成 1m 已确认信号 |
| 参与 `V` | 永续 `volCcyQuote`/历史时段同口径基线（12h USDT 总额用于池，而短窗 15m/1h 用于因子）；`z(log1p(V))` | 放量增强现有方向的可信度，本身没有买卖方向；低价高枚数不能按 base 单位误作大额 |
| OI 基币变化 `O` | OKX `oiCcy` 的 `Δlog(oiCcy+ε)` 或正确合约面值换算；15m/1h；检查同一合约规格版本 | OI 增加仅说明未平仓规模增长；`oiUsd` 随价上涨不是开新仓证据；不显示“新多/新空数量”臆测。[OKX OI](https://www.okx.com/docs-v5/en/#public-data-rest-api-get-open-interest) |
| OI×价格 `C_oi` | `sign(T) × clip(max(0,O),0,1)`，可受完整度/流动性门控；价涨+OI 涨增强多头方向**候选**，价跌+OI 涨对称 | 不推断哪一方主动开仓；OI 不涨不等于必定反转，负 OI 不给反方向自动加分 |
| 量×价格 `C_vol` | `T × max(0,clipped volumeSurprise)`，和趋势项同组设总贡献上限 | 大阳线+放量不能作为两个独立正交因子无限叠加；要控制波动和同一价格冲击 |
| 现货/合约差 `D` | 同时刻 spot/perp 回报差、spot/perp quote turnover 比值与各自历史基线；同单位、交易对映射，必要时比较 mark/index | 现货支持可作佐证；单一 OKX 现货不代表全市场，合约与现货不同深度/交易时段需控制 |
| 主动流 `A_spot/A_perp`（可选） | **有完整主动方交易数据时**：`(takerBuyQuote-takerSellQuote)/totalQuote`，分 spot/perp；需要确认 OKX 频道数据是否完整、`side` 语义 | 总 volume 不能冒充净买入；只订阅高分币的 trades-all 不得令它们在全市场榜单获得额外权重 |
| 基差/拥挤 `B` | `log(perpPx/spotPx)`、mark/index 差、funding 预测值的过去分位、`oiCcy/quoteLiquidity`；分组限权 | 正费率不等于必跌；费率/基差相关，防止重复加/减分。结算期以 `fundingTime/nextFundingTime` 为准。[OKX funding](https://www.okx.com/docs-v5/en/#public-data-rest-api-get-funding-rate) |
| 执行成本 `C_exec` | 指定下单规模的即时买卖价/盘口深度模拟冲击、双边手续费、可能跨结算点的资金费 | 必须基于计划名义和持有期，否则“流动性大”也可能买不到目标价；主要是 gate/净价值，不是方向 alpha |
| 事件 `E`（后续可选） | 有源的公告/解锁计划数量/已知流通量、版本与首次可见时刻，区别公告/计划/实际转账 | “将解锁”是供给风险背景，不能自动给空头加分；无法回溯公告当时版本的事件不进历史回测 |
| 集中度 `H`（后续可选） | token/chain 地址分类后的可流通受益集中度区间，不确定项保留区间/未知 | 地址不等于个人/账户；交易所钱包、桥、锁仓不能被当作同一“巨鲸抛售方向” |

### 价格涨、成交量大、OI 没跟：正确的系统行为

1. `T>0` 且短窗 `V` 高，但 `O≈0`/下降：标 `unconfirmed-leverage-move`（**未被 OI 扩张确认**），降低趋势的 **确认项**，不宣称“刷量造假”；若现货活跃/主动流支持，仍可能是现货驱动，不能一刀切扣多头全部分。
2. 若永续量激增但现货量/主动流不跟随，且基差/资金费走极端：单独标 `perp-spot-divergence` 与方向性拥挤**软惩罚**；无 spot 映射时为 unknown，不能凭空计算。
3. 做空不等于多头被扣分。一个明显上升趋势即使“不够确认”，也不自动产生高做空分；做空需要自己的下行证据、执行可行和回测支持。

## 3. 可解释 v0 公式与“可比性”

假设所有输入仅从可得历史拟合、截断在 [-1,1]；参数仅作**首轮实验起点**而非“最佳权重”。以 `d=+1/-1` 表示多/空：

```text
coreEvidence[d] = d * (0.45*T + 0.30*C_oi + 0.25*C_vol)
penalty[d]      = 0.30*crowding[d] + 0.25*volumeOiMismatch[d]
                  + 0.25*carryCost[d] + 0.20*executionCostRisk[d]
entryCore[d]    = clamp(50 + 50*coreEvidence[d] - 20*penalty[d], 0, 100)
```

`volumeOiMismatch` 在 `entry-core-v0` **只使用全池可得的永续价格、永续成交额与币计 OI**；现货背离是单独标签或 `entry-spot-v0` 的特征，不可让只有现货映射的币在 core 榜单被额外扣分。`executionCostRisk` 在 core 仅基于同覆盖的 ticker bid/ask 点差，深度冲击评估属于后续的独立交易许可/净价值；不把仅为选中币订阅的盘口资料偷偷混进全市场分数。`C_oi` 和 `C_vol` 与 `T` 共源，应额外加一层**趋势/确认组总贡献上限**，防止同一冲击重复放大；此公式只说明结构，组上限及归一化随模型版本固定。`crowding` 以方向性过热分位/持仓相对深度为主，`carryCost` 是持仓期可能需要支付的费率占风险预算（不把正费率直接判空）；`executionCostRisk` 是仅在硬门槛内仍有非零的成本惩罚。软惩罚均为 [0,1]；单一因子异常和缺失的处理见下。若看到旧[初步报告](../go-okx-personal-trading-research-2026-09-19.md)的另一套示例权重，**以本蓝图作为后续实验的版本化候选**，而不是把两份草案叠加。

**一个必要的覆盖率修正**：第一版建议把缺失 spot 因子置 0、降低 confidence；实际未验证前不能在“完整模型”和“缺失模型”之间即兴切公式。因此：

- `entry-core-v0` **只含全市场可按定义采到的价格、成交额、币计 OI 等核心因子**；OI/价格严重过期时为 `UNRATED` 不进入有效分排序。
- `entry-spot-v0` 在现货有可靠映射、覆盖率和历史验证达到门槛后，加入现货主动流/差价佐证，以**新的模型版本和单独可比池**展示；原全市场默认排序仍用 core 分，不能混排 `spot` 版与 `core` 版。
- 新闻、解锁、持仓集中度初期只做**证据/风险 tag**；量化信号必须有完整覆盖/历史版本/消融增益，才考虑加入某个新版本。高分不消除其硬风险提示。
- `quality = {available, freshness, source, validationStatus}` 是与 score 平行的字段，不乘进一个看似概率的综合数。可选特征缺失只能选**预先训练/验证过的 fallback 模型**或退回 core；不得在生产中随意填 0 或把剩余权重再归一化。

每条 `ScoreCard` 保留 `core/spot modelVersion`、标准化/贡献向量、原始窗口、as-of/source/缺失和成本假设。`entryLong` 和 `entryShort` **不要求加总 100**：方向性执行成本和风险惩罚可以同时拉低两者；这不是互斥结果的概率模型。

## 4. 先 Gate，再在未来研究真正的“进场价值”

```
Instrument/live? 12h turnover complete? source freshness? OI unit/mapping valid?
          └─ no → UNRATED / BLOCKED_NEW_RISK
          └─ yes → direction evidence score (two directions, rank only)
                       ↓ separate execution/cost check
            spread/depth/mark/slippage/fees/funding / portfolio exposure gate
                       ↓ if in future validated: netEdge & alerts
```

核心数据/合约规格/账户状态异常、盘口对目标名义的冲击超过预算、接近强平或超过组合风险阈值，均独立阻止新增仓；**不要设定某个“70 分就买”**。标记价用于清算/保证金风险、永续成交价用于交易成本、现货价用于差异分析；不能混为同一个价格。如果将来训练概率模型，需要定义互斥的“4 小时内首先触及净收益上障碍/下障碍/均未触及”标签及可执行成交价，独立校准后才可输出 `p_up/p_down/p_neither` 和经成本计算的 `netEdge`。与当前未校准的排名指数并列，不覆盖其语义。

## 5. 只读仓位的离场，不是反转入场分

`PositionReader` 获取专属仓位快照；`pos` 是张数须乘当时合约规格，考虑净/双向模式、保证金模式、当前 mark、预估强平价、历史成交与资金费。对现有多/空仓分别输出：

- `continue-to-hold`：在当前持仓规模、剩余风险预算与下一个评估期假设下的继续持有证据/成本；和“现在退出、可能再入场”的情景**分别估**。没有经过样本外价值验证时只做风险等级/理由，不输出精确预计收益。
- `protect-now`（硬提醒）：强平距离、保证金不足、交易所停牌/报价异常、流动性枯竭、数据/仓位源不可靠。这一类警示不依赖入场高低分；只读工具不替用户修改/撤销已有交易所保护订单。
- `review/reduce-risk`（软信号）：现货流转向、OI 规模急变且价差/波动恶化、资金费成本变化、关键公告修订、趋势失效、原先的入场假设已不成立。优先解释“证据转弱”而不是伪装成精确止盈价。
- `exitUrgencyLong/Short` 可以是独立 0–100 风险优先级（**非当前回报概率**），带来源/时钟和分档阈值；与入场分用不同输入、校准和验收。止盈/止损可展示预设亏损预算/收益目标下的**情景区间**及预估滑点，不代发任何订单。
- 为了避免在阈值附近反复提醒，退出与重新观察应有不同触发阈值、连续性和冷却，但不能改变硬风险事件响应。私有 API 断连状态不能解释为“仓位已平”。

## 6. 回测数据、标签与反过拟合流程

1. **建当时可见数据**：上市、退市、预市场、合约面值变更和历史当时 universe；每条价格/OI/费率/新闻/解锁有 `eventTs/receivedAt/availableAt/revision`，只用决策前可见版本。只回测今天仍在 top200 的币是幸存者偏差。公开的 OI 历史不足时从本系统起持续采样并标注可用区间，不用“今天拉来的历史最终数据”伪造当时预测费率。
2. **标签/执行**：做多从信号后可成交卖盘（ask）计入，平多按 bid；空头方向相反；含双边手续费、点差/冲击、容量、跨越实际 fundingTime 的费率成本、未成交/部分成交与标记价强平风险。OHLC 只有高低点同时触及止盈/止损而无先后信息时，采用保守顺序或剔除，不择有利路径。
3. **验证顺序**：固定假设/特征→按时间滚动训练→独立校准→未触碰后验测试→实时 shadow；标签重叠窗口在训练/验证切分处 purge/embargo。币、波动、流动性、上市早期分组报净收益/尾损/容量/置信区间；和不交易、仅趋势、无 OI、无现货、无事件、不同延迟/滑点的基线逐项消融。记录每次调参/策略版本，避免反复试到“最好回测”。[回测过拟合研究](https://papers.ssrn.com/sol3/papers.cfm?abstract_id=2326253)
4. **成本后门槛**：直到不同市场状态/样本外可复现增量净优势，并且数据覆盖、执行容量、最大回撤、已实现/预测成本在预设预算内，才允许进入提醒功能的另一次审批；否则只显示“研究分”。概率预测若实现报告校准/Brier/分箱样本数，不能用高 AUC 替代净可交易收益。
5. **线上线下一致性**：录制带接收时间/修订的公共消息，同一 Go adapter 标准化/FeatureEngine/ScoreEngine 分别走 replay 和 live，逐条比较核心因子、分数、资格和 gate；测试 1m `confirm=0→1`、REST 时戳倒退、OI 乱序、合约面值变动、funding 结算边界、解锁公告更改、WS 缺口及 reconnect。若不一致，不得拿该回测证明线上信号。

## 7. 方案扩展与仍不确定的研究假设

- 可增添 `BTC/ETH 相关性及剔除大盘后的 idiosyncratic return`，避免把大盘同涨误算个币优势；但相关性随市场状态变化，须单独做样本外验证。作为详情参考可先于评分引入。
- 现货相对强弱、OI/成交额比、基差 z-score、资金费变化的交互比生硬阈值更可解释；然而潜在共因是市场波动和杠杆挤压，需对价格/波动/大盘收益控制并做滞后测试。
- `liquidity-adjusted score` 可用执行成本作为**独立排序次序或 gate**，但不要把 score 伪装成净预期收益。不同名义规模的滑点不同，界面须显示用户所选择的名义规模。
- 供应解锁、链上持有人、新上市、公告可先做**风险视图与警示**；建立 token-chain 映射、地址归因、首次可见历史样本后才有资格研究是否加入方向模型。缺可靠来源显示 `unknown`，不是“安全”。
