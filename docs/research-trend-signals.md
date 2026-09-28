# 趋势/方向信号调研（2026-09-28）

> 起因：用户反馈多空分“无法指示趋势、照着做会亏钱”，希望有对山寨币相对 BTC/ETH 更有用、反应更快的算法。
> 结论先行：**没有公开算法能稳定预测小时级涨跌**；业界做法是把若干有文献支撑的信号做成可检验的因子，用样本外 IC 和扣成本收益决定用不用。本次把其中能用现有数据计算的几类加入事实面板与对照规则，交给“规则与战绩”检验。

## 1. 现状诊断（本机 14 天回放，2026-09-13 → 09-27，约 80 个币/横截面）

| 规则 | 1h 多头 IC (t) | 4h 多头 IC (t) | 解读 |
|---|---|---|---|
| 趋势+OI 确认（默认规则） | −0.042 (−4.8) | −0.059 (−3.5) | **方向相反**：多头分越高，之后 1–4h 相对越弱 |
| 短期反转（相对 BTC） | +0.043 (+4.0) | +0.068 (+3.2) | 同一信息的反向；扣手续费/点差后前 20% 仍很薄 |
| 资金费倾向 | +0.004 | +0.004 | 无信号 |

在这段样本里，**小时级山寨永续是均值回归（反转）而不是趋势延续**。“追涨 + OI 放大”的默认规则因此系统性地站错边——这与用户的实际体验一致。
注意：14 天只是一个行情阶段，结论可能随阶段翻转，所以需要持续的样本外记录。

## 2. 业界方法速览

| 类别 | 代表做法 | 证据与适用周期 | 本项目 |
|---|---|---|---|
| 时间序列动量 (TSMOM) | 过去 N 期收益符号/波动率归一化的均线差（MACD 类）| Moskowitz, Ooi & Pedersen 2012；Baz 等 2015（CTA 常用的 EMA 交叉信号）；加密货币中 1–4 周动量显著（Liu & Tsyvinski 2021）| 新增 `trend_ema`、规则“趋势跟随（EMA + 效率比）” |
| 横截面动量 / 反转 | 池内按过去收益排序做多强空弱，或反过来 | 加密货币周级动量（Liu, Tsyvinski & Wu 2022）；日内到日级常见反转 | 已有“短期反转” |
| 残差（β 中性）动量/反转 | 先回归掉大盘（BTC）β，再按剩余收益排序 | Blitz, Huij & Martens 2011：残差化后信号更稳、回撤更小。山寨大部分涨跌是 BTC 带动，不剔除就会把“高 β”误当“强势” | 新增 `beta_btc_48h`、`resid_1h/4h/15m`，规则“残差反转（β 中性）”“快速残差动量” |
| 趋势/震荡状态过滤 | Kaufman 效率比、ADX、Hurst 指数 | 趋势信号在震荡里反复止损是亏损主因；先判断“是否在走趋势”再用趋势信号 | 新增 `eff_ratio_24h` |
| 订单流失衡 | 主动买卖差（Delta）、盘口失衡 OFI | Cont, Kukanov & Stoikov 2014：OFI 与同期价格变化近似线性；预测力只在秒到分钟级，很快衰减 | 已有 Delta/CVD 展示；作为评分字段需逐笔录制覆盖全池，记入待办 |
| 日内时段动量 | 开盘段收益预测收盘段 | Gao, Han, Li & Zhou 2018（股票）；加密 24h 交易，时段效应弱 | 不做 |
| 机器学习（GBDT/LSTM 等）| 多因子非线性组合 | 需要长样本与严格的滚动样本外验证，否则极易过拟合（Bailey 等 2014）| 等 ≥30 天样本外数据后再议（见 roadmap 评分方法） |

## 3. 为什么“反应快”不等于能赚钱

- 越短的周期，信号越弱、换手越高：15 分钟级的优势通常不到 0.1%，而一次开平的手续费 + 点差 + 滑点已经在 0.1%–0.2%。表里“扣成本后前 20%”一栏就是为此设的。
- 快速信号更容易被噪声触发，需要配合状态过滤（效率比）和更严格的门槛。
- 因此“快速残差动量（15m）”只作为对照；它的字段在整点回放里没有，只能从上线后开始积累战绩。

## 4. 本次改动

- 新字段（均为无方向的事实，不改变已有字段口径）：`beta_btc_48h`、`resid_1h`、`resid_4h`、`trend_ema`、`eff_ratio_24h`（可回放）；`ret_15m`、`resid_15m`（仅实时）。
- 新对照规则：残差反转（β 中性）、趋势跟随（EMA + 效率比）、快速残差动量（15m，仅实时）。全部未经验证，**不是推荐**。
- 默认评分规则改为“残差反转（β 中性）”（设置页可改；浏览器只记住手动选择的规则）。

回放结果（14 天，09-14 → 09-28，约 80 个币/横截面，多头 IC 与 t 值；“净”为多头前 20% 扣成本后的平均超额）：

| 规则 | 1h IC (t) | 4h IC (t) | 4h 前 20% 净 |
|---|---|---|---|
| 趋势+OI 确认（默认） | −0.043 (−5.0) | −0.063 (−3.7) | −16 bps |
| 趋势跟随（EMA + 效率比） | −0.020 (−2.3) | −0.045 (−2.3) | −7 bps |
| 短期反转（相对 BTC） | +0.043 (+4.0) | +0.066 (+3.3) | +18 bps |
| 残差反转（β 中性） | **+0.048 (+4.7)** | +0.062 (+3.2) | +14 bps |

经典趋势跟随在这段样本的小时级同样站错边，进一步说明当前是反转占优的阶段；β 中性化让 1h 反转信号略干净。所有正向结果扣成本后都很薄，空头一侧扣成本后为负。

## 5. 怎么用

1. 到“规则与战绩”看各规则的回放与上线后 IC：t 值绝对值 ≥ 2 且扣成本后收益为正，才算有初步证据。
2. 显示“方向相反（显著为负）”的规则，说明应当反着理解，而不是照做。
3. 分数只是池内排序，不是胜率、也不是下单建议。

## 参考文献

- Moskowitz, Ooi & Pedersen (2012). Time series momentum. *Journal of Financial Economics*.
- Baz, Granger, Harvey, Le Roux & Rattray (2015). Dissecting investment strategies in the cross section and time series. SSRN.
- Blitz, Huij & Martens (2011). Residual momentum. *Journal of Empirical Finance*.
- Liu & Tsyvinski (2021). Risks and returns of cryptocurrency. *Review of Financial Studies*.
- Liu, Tsyvinski & Wu (2022). Common risk factors in cryptocurrency. *Journal of Finance*.
- Cont, Kukanov & Stoikov (2014). The price impact of order book events. *Journal of Financial Econometrics*.
- Gao, Han, Li & Zhou (2018). Market intraday momentum. *Journal of Financial Economics*.
- Kaufman, P. *Trading Systems and Methods*（效率比 / KAMA）。
- Bailey, Borwein, López de Prado & Zhu (2014). Pseudo-mathematics and financial charlatanism. *Notices of the AMS*.
