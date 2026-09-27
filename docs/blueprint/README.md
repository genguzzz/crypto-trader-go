# OKX 个人交易研究工具：端到端蓝图（研究补充）

> **2026-09-26 修订：**本文部分假设已被公共接口实测修正（候选池规模、股票/商品永续、tickers 带宽、评分方向、preview 分、交付顺序等），冲突处以[蓝图修订](revision-2026-09-26.md)为准。

> 2026-09-19 · 仓库 `~/Code/crypto-trader-go` · **规划而非已实现功能**。本文补足[第一版粗略调研](../go-okx-personal-trading-research-2026-09-19.md)没有说明的逐功能技术路径。每项都写到可被原型和 Go 实现逐一验收；凡性能、预测力或 SDK 覆盖尚未实测之处，都标为待 PoC，而非事实。

## 一页决策

1. **产品不是下单机器人。** 核心闭环是“发现候选 → 验证可交易/数据质量 → 双向证据与排序 → 图表/事件/仓位解释 → 历史与前向验证”，并且有独立的告警/退出风险；自动下单须另立审批、风控、权限和失效方案。
2. **范围**：默认 live、非 pre-market 的 `*-USDT-SWAP`，对全池计算**连续 720 根已确认 1m K 线**的 `volCcyQuote`（USDT），阈值 `>10,000,000`，按滚动 12h 成交额排序取前 200；没有完整窗口的币不伪装为合格而进“新品/预热观察栏”。阈值、Top N、WS/REST 周期、评分窗口可配置。前 200 ≠ 保证恰有 200。支持单独自选/持仓关注，不改变官方候选池定义。[OKX instruments](https://www.okx.com/docs-v5/en/#public-data-rest-api-get-instruments) · [candles](https://www.okx.com/docs-v5/en/#order-book-trading-market-data-get-candlesticks)
3. **5 秒是状态协调与发布时钟，不是每 5 秒重新抓 200 个币的 720 根 K 线，更不是 5 秒精确 12h 成交量。** 全市场 ticker 可按 5s REST 采样、WS 增量维护当前价/盘中 K 线；确认的成交额榜只在分钟柱闭合且补齐时改变。5s 的盘中分展示 `preview`，1m 收盘后存 `confirmed`；两个分数不混作可回测信号。[OKX rates](https://www.okx.com/docs-v5/en/#overview-rate-limits) · [ticker](https://www.okx.com/docs-v5/en/#order-book-trading-market-data-get-tickers)
4. **Go 单进程模块化起步**，明确可替换 OKX adapter，REST 回填＋按 public/business/private 分开的 WS，单一 `FeatureEngine`/`ScoreEngine` 供线上、回放共用；个人本地 SQLite WAL（经 PoC 决定）＋ Vue/TS。行情落盘和 API 页面解耦但不预设 Redis/微服务。第三方新闻/解锁/链上持仓严格独立于 OKX 原生源；“大盘”默认 BTC/ETH 代理，可选本项目点时成员明确的 OKX USDT 永续篮子而非全市场市值指数。
5. **评分 = 入场候选质量，不是概率/建议。** `entry_long`、`entry_short`、持仓 `exit_long`/`exit_short` 四个不同对象；核心分可跨候选比较，缺失特征和外部证据不会偷偷改变同榜单公式。OI 不能推断“庄家”方向，解锁不能推断必跌。详见[评分和验证](scoring-and-validation.md)。

## 逐项查阅

| 问题 | 入口 |
|---|---|
| 用户提出的每个功能点，用什么数据和技术，失败时如何呈现？ | [功能追踪矩阵](feature-traceability.md) |
| 行情/得分/页面怎样形成可运行系统、API/表/限频/重连怎么设计？ | [运行架构与数据契约](runtime-and-data-contracts.md) |
| 多空/离场怎么算分，现货和合约怎么比较，如何证明不是过拟合？ | [评分与验证协议](scoring-and-validation.md) |
| 哪些开源库值得复用，哪些只参考，数据源限制是什么？ | [开源选型和数据来源](oss-and-data-sources.md) |
| 原型界面到底有哪些页面、什么状态、怎么解释分数？ | [页面流程与原型验收稿](product-flows-and-prototype.md) |
| 哪些假设被实测推翻、定稿方案和待你决定的事？ | [**蓝图修订 2026-09-26**](revision-2026-09-26.md) · [PoC 探针](../../research/poc/README.md) |
| 原型/实现的依赖顺序和“做完”的标准？ | [分阶段交付与决策门槛](#阶段交付与门槛) |

## 先说清的产品术语

| 术语 | 明确语义 |
|---|---|
| `universe` | 量化可选全池，包含不达标的 USDT 永续；`active` 是达标且前 200；`watchlist` 是用户自选；`held` 是只读账户的持仓。四者不互相伪装。 |
| `turnover12h` | 连续 720 根确认 1m `volCcyQuote` 的分钟精度窗口；不包含尚未确认柱；出现缺口时 `warming/stale`。不同 `asOfMinute` 不直接比较排名。 |
| `preview` / `confirmed` | 前者每约 5 秒以当前行情计算，会重绘；后者在完整分钟柱确认后固定一个模型版本和信息截点。决策依据仍需回测验证。 |
| `score` / `confidence` / `risk` | 分数是有方向的候选证据；置信度是数据完整/及时/模型验证状态；风险 tag/硬门槛不会被高分抵消。分数不等于未来盈利概率。 |
| `OI` | 全市场未平仓**多空双方共同头寸**；`oiCcy` 的变化是未平仓规模变化，不能仅凭 OI 判定主动多/空、操纵或下一根方向。[OKX OI](https://www.okx.com/docs-v5/en/#public-data-rest-api-get-open-interest) · [CME 释义](https://www.cmegroup.com/education/lessons/open-interest) |
| `holder concentration` | 现货链上 token 地址分布（非 OKX 现货账户真实受益人分布）；托管/桥/合约/锁仓须剔除或标未知。 |

## 阶段交付与门槛

| 阶段 | 交付切片 | 入口条件 → 可验收退出条件 |
|---|---|---|
| R：本次强化研究 | 逐功能蓝图、Go 库可替换原则、数据源与 PoC 计划 | OpenSpec strict validate、链接检查、边界/缺失/配额定义一致；**不声称 PoC 性能已达标** |
| P：交互原型 | 合成快照＋增量事件，桌面列表/详情、移动路由、四向分数解释、风险/断线/未知/未连接态 | 用户逐页确认信息密度、K 线相对收益基点、榜单增删/选中状态；不接真实账户 |
| I1：基础数据 | Go 公共 API/WS 适配、限流器、窗口回填、断连补洞、全池候选 | 200 路与全池订阅 PoC、配额/新鲜度/缺口指标及故障注入通过；不能通过则调整 SLA 并明确告知 |
| I2：榜单与可视化 | 标准化特征、规则分 v0、版本化快照、HTTP/SSE、图表详情 | replay 与 live 因子一致；名单阈值、排序、方向/缺失筛选、断线恢复 E2E |
| I3：研究验证 | 历史当时可见数据、基准/成本回测、walk-forward、shadow | 收益与坏情境报告；未达门槛只显示实验分和风险提示，不触发交易提醒 |
| I4：只读仓位/外部事件 | 最小权限 OKX 私有读、仓位独立退出分、可信新闻/解锁/链上来源 | 只读权限和网络/日志安全测试、净/双向持仓与强平/费用边界测试；无字段就 `unknown` |
| X：自动交易（**未纳入本项目承诺**） | 独立提案、最小交易权限、风控/熔断/dry-run 和审批 | 只有新的明确授权才评估；不能由“分数高”自然演进为下单 |

## 可逆默认和真正需要用户决定的事

- 默认 Top200 定义为 **12h USDT 成交额前 200 且超阈值**，而不是另一个 OKX “热门榜”；若指定官方热门榜，必须重新明确来源/排序与截断关系。
- 默认 5 秒为榜单状态发布/行情预览，确认窗口只按分钟推进。要逐秒精确窗口，需要 400+ 路完整逐笔存储、断连回补与性能预算；不得宣称这两者等价。
- 默认单用户本地部署、单浏览器/SSE；外网访问需 HTTPS/认证/最小权限。仓位只读接入须单独确认。
- 默认信号研究的预测/持有期取 4h **仅作为预注册候选配置**；无需此时承诺参数有效，前向检验之前不设“最佳进场”结论。
