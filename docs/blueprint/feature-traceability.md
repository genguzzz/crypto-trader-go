# 功能追踪矩阵：需求 → 实现 → 验收

> **2026-09-26 修订：**本文部分假设已被公共接口实测修正（候选池规模、股票/商品永续、tickers 带宽、评分方向、preview 分、交付顺序等），冲突处以[蓝图修订](revision-2026-09-26.md)为准。

> 本文描述**计划**，不是已上线状态。路径/表/API 指向[运行契约](runtime-and-data-contracts.md)，公式/风险指向[评分](scoring-and-validation.md)，数据源和库指向[选型](oss-and-data-sources.md)。MVP/I1–I4 的阶段定义见[总览](README.md#阶段交付与门槛)。`OKX` 列没有列出的内容不可写“OKX 原生数据”。

## A. 合约池、实时榜单与主动监控（原需求 1、2）

| ID / 体验与可配项 | 来源与精确口径 | Go 处理 → API/界面 | 异常/验收（拟实现） | 切片 |
|---|---|---|---|---|
| A01 可交易合约目录 | OKX `/public/instruments?instType=SWAP`，`instId/state/settleCcy/ruleType/ctVal/ctValCcy`；默认 live USDT 线性永续且非 pre-market | `InstrumentCatalog` 定时全量＋WS instruments，版本化映射；`/api/v1/instruments`，UI 只显示有效状态 | 停牌/规则变更立即失去新增仓候选资格；用模拟上下架验证 | I1 |
| A02 12h USDT 流动性过滤 | 全池每币连续 720 根 confirmed 1m `volCcyQuote`；`>` 1000 万，USDT；允许 `minTurnover/maxRank/window` 配置 | `CandleWindow` 增量 O(1) 滚动和＋完整性位；`UniverseService` 一次原子快照；`/api/v1/universe` | 缺任一分钟、上市不足 12h、乱序不视为 0 成交额；暴露 `warming/stale`。固定样本检测边界=1000 万时不达标 | I1–I2 |
| A03 热门前 200 与扩容/缩水 | A02 后按同一 `asOfMinute` quote turnover 降序，ID 稳定排序；达标不足 200 显示实际 | `topN` 参数校验、固定 tie-break；榜单默认再按规则分排序，但**候选池筛选仍由成交额决定** | 新币进入/离开不需要重启；榜单在 200 边界、等成交额、重连后 deterministic | I1–I2 |
| A04 约 5 秒名单核对 | 每 5 秒一个全市场 REST ticker 作为辅助发现/诊断，WS `candle1m` 维护全池窗口；`confirm=1` 才更新 A02；`instruments` 新增跟踪 | `Reconciler` 5s tick 发布 seq + `added/removed/updated`；SSE，表头显示 `checkedAt/turnoverAsOf` | 任一 K 线缺口标 stale 并补洞；REST 时间倒退拒覆盖；5s 不承诺分钟确认成交额变化 | I1–I2 |
| A05 实时价与盘中指标 | WS tickers、1m 未完成 K；价格/ticker 与 confirmed 收盘分离 | `MarketState` latest ticker 更新 + `PreviewScorer` 每 5s；页面 `preview` 徽章 | 没有新消息不伪造价格变动；`receivedAt` 超阈值显示陈旧；模拟断流/乱序 | I1–I2 |
| A06 自选/保留已移出的币 | 用户偏好与持仓，和 `active` 集合独立；不写交易所 | `WatchlistStore`、`HeldOverlay`；SSE removed 事件、详情路由独立于排序 | 移出 active 后自选/持仓仍可看详情并可显示“非活跃”；不能冒充候选分 | P/I2/I4 |
| A07 方向筛选/多维排序 | 四个分数、12h 成交额、价涨跌、OI 变化、资金费、风险/新鲜度 | `ScoreSnapshot` 稳定排序；`?direction=long|short|all&sort=score|turnover|oi|funding&filters=...` | 缺失值排尾，未知不是零；选中项目不随排序跳动；筛选/排序组合 E2E | P/I2 |
| A08 多条件过滤器/配置 | 用户级 JSON 配置，范围校验：阈值、N、score 版本、时间窗、提醒静音，不接受任意脚本 | `ConfigService` 校验后原子版本替换；公开榜单带 configVersion | 改配置触发重算与 SSE reset；非法阈值拒绝、不影响当前配置 | I2 |
| A09 新上市观察栏 | 未满 720 完整分钟、预市场、无现货映射等 | `DiscoveryWatch` 与 active 分离；展示上市时间、当前 24h 估值但不当 12h | 不足历史不凑排名/不触发自动提醒；待窗口完整可进入 active | P/I2 |

## B. 评分、分解、提醒（原需求 2 与用户的量价/OI/现货设想）

| ID / 功能 | 数据/算法 | 处理 → 展示/接口 | 异常/验收 | 切片 |
|---|---|---|---|---|
| B01 独立多空入场分 | 同一时刻可用价、确认 K、币计 OI、量、费率、点差；分别 `entry_long/entry_short`，不互为 100 的补数 | `FeatureEngine` → `ScoreEngine` 版本化 `ScoreCard`；`/api/v1/scores`；默认排序 max 两方向 | 同时输出 `reasons/penalties/freshness/modelVersion`；指定 fixture OI+价涨使空头证据下降，非绝对禁空 | I2 |
| B02 价格/成交量/OI 联合证据 | `oiCcy` 百分变化、价收益、USDT quote turnover 的历史标准化；交易量**不表方向** | 因子组 `trend/participation/positioning`，交互项而非三次重复计分 | 价涨成交大 OI 未确认：不能称“造假”；显示 `unconfirmed-move`，做多减少趋势确认、做空不自动加分 | I2 |
| B03 现货↔合约差异 | 同基础资产 USDT SPOT 映射、两市场同周期 K 线、成交额、可得时 taker side；`basis=(perp/spot)-1`、相对收益/量比 | `SpotMapper` + `BasisFeatures`；详情差异卡；有覆盖/校准后才纳入相同模型版本 | 没有同口径现货/数据延迟过大时 `unknown`，不填 0；逐笔 flow 只对有完整样本的币计算 | I2–I3 |
| B04 OI 与资金费/基差 | OKX OI 张数/`oiCcy`/`oiUsd`，funding 的预测与结算时间，现货价格/mark | `PositioningFeatures`、资金费预期成本、拥挤标签；详情时间序列 | 对币数 OI 求变化；价格导致的 `oiUsd` 增长不能叫“新增仓”；结算频率不写死 | I2 |
| B05 评分解释/版本/数据质量 | 规则 ID、原始值、标准化、方向增减分、实际可见时刻与训练版本 | API 返回贡献数组，UI 展开“为什么”；历史快照可回放 | 缺关键因子 `unrated`；可选因子缺失用**验证过的 fallback 模型**或只显示核心分，不临时归一化 | I2–I3 |
| B06 硬风险门控、软惩罚 | 停牌、过期、价差/滑点上限、合约规格不明是 gate；费率成本/拥挤/事件风险是有上限惩罚 | `EligibilityGate` 与 `PenaltyEngine` 分开，门控优先 | 极高趋势分不得覆盖不可交易或强平风险；fixture 断开/点差跳变显示 blocked | I2–I4 |
| B07 关注提醒与节流（可选） | 确认分过已**验证**阈值、置信度、连续观察、冷却、状态边沿 | `AlertPolicy` 消费 score snapshot；站内通知优先，外送需另设渠道 | 同一规则/币/方向去重，preview 不推“确认入场”；未完成回测默认禁策略提醒 | I3 |

## C. 合约详情、图表和外部资料（原需求 3）

| ID / 功能 | 数据/口径 | 处理 → UI | 异常/验收 | 切片 |
|---|---|---|---|---|
| C01 合约自身 K 线与多周期 | OKX 1m 基础柱，按 UTC 对齐聚合 5m/15m/1h/4h，存 OHLCV/confirm | `BarAggregator` → `/api/v1/candles?instId=&bar=&from=&to=`；图表平移/缩放 | 盘中未确认柱虚线/提示；缺柱显示空档，不能将前价补成真实 K 线 | I2 |
| C02 叠加现货合约 K 线 | 同基础币的真实 spot instId；单币图可用两系列＋独立价轴/成交量 | `InstrumentMapping` 手工覆核例外；图表切换“蜡烛+现货” | 映射无效/上市时间不同用 missing/warm-up，不画虚假 0 值；两市场各保留来源 | P/I2 |
| C03 BTC/ETH/大盘比较 | BTC-USDT、ETH-USDT；可选本项目用前一日已知成员构造的 OKX USDT 永续等权市场篮子（不叫全币市值指数）；锚点 t0 同时可见收盘价归一 100 | `ComparisonService` 公共时间轴取交集；市场篮子成员/权重按当时快照固定，独立归一化相对收益视图 | 不把 BTC 绝对价格与小币贴同价轴；基准/篮子缺时间窗口时标缺口；不得把 BTC/ETH 代理标成全市场 | P/I2 |
| C04 深度/流动性与盘口 | OKX 盘口快照、bid/ask/spread、指定名义规模的价差和订单冲击估计 | `BookCache` 仅给选中/高分/持仓币订阅，需 snapshot+增量/序列校验；详情卡 | book 序列不连续立即重快照，过期禁算滑点；不在全 400+ 永久订阅 L2 | I2–I4 |
| C05 资金费/持仓量/多空统计 | OKX funding/OI/适用统计端点；第三方数据单列 | `SeriesService` 带 `period/source/asOf`、单位转换；详细指标区 | OI 历史不够即标覆盖不足；“多空比”账户比≠仓位比，分开命名 | I2–I3 |
| C06 新闻/催化事件 | 可核来源 API/RSS、文章 ID/发布时间/首次采集时刻 | `ContentIngest` 去重、symbol/实体消歧；事件时间线只作证据 | 传闻/转载无原源 tag；不可复现发布时间的事件不纳入历史信号 | P/I4 |
| C07 解锁日期/供给变化 | 项目方计划、实际链上释放、计划修订与流通量来源 | `UnlockProvider` 日期、数量、币、chain、公告版本和覆盖率 | 数据缺失=unknown，不能当“无解锁”；计划不是实际售出；不默认加空头分 | P/I4 |
| C08 现货持有人/集中度警示 | 链上 token 地址、地址实体标记、流通量和托管钱包归因（第三方） | `HolderRiskProvider` 地址合并和 chain mapping；`concentration-unknown`/高集中度 tag | 地址分布**不是 OKX 托管现货持仓分布**；归因不可信只标未知；指数不进方向分直到实证 | P/I4 |
| C09 链上/代币经济等扩展 | 流通/FDV、合约安全/DEX 流动性、已证实项目方公告 | Provider 接口＋源/许可/发布时间标注；详情可折叠模块 | 第三方额度/源错/同名币歧义时不给错误“零风险”结论 | I4+ |

## D. 我的仓位、退出与安全（原需求 4）

| ID / 功能 | 数据/算法 | 处理 → UI | 异常/验收 | 切片 |
|---|---|---|---|---|
| D01 只读 OKX 账户连接 | 专用只读 API key、IP 限制；`/account/positions` 快照 + 私有 WS；绝不授予交易/提现 | `PrivateAdapter` 与公共数据边界分离；本地加密存放密钥由用户配置、不输出日志；`/api/v1/positions` | 原型使用合成仓位；鉴权失效仅显示 disconnected，不假装空仓；审计权限配置 | I4 |
| D02 仓位与风险状态 | `pos/posSide/avgPx/upl/liqPx/margin/mgnMode`、mark/index/费率；合约张数换算 | `PortfolioState` 处理净/双向模式，`PositionSnapshot`；仓位页含敞口、浮盈亏、成本 | 全仓强平距离受其他仓位/余额影响，展示交易所估算且标风险；缺值 unknown | I4 |
| D03 独立离场评估 | 仓位方向和入场价、变化后的现货/OI/趋势、接下来持仓资金费、当前点差/退出冲击及离清算距离 | `ExitAssessment` 输出 protect/hold/review/exit-risk、`exit_long/exit_short` 解释，绝不反转入场分 | 数据过期/账户掉线停建议且风险醒目；止损/止盈只给情景收益/风险估算，不代发订单 | I4 |
| D04 持仓 vs 榜单 | 仓位不要求属于 active；对应合约详情仍可查 | `HeldOverlay` 列表置顶/筛选 “我持有的”，详情保留最后可靠快照 | 低流动性/下架持仓显著提示；不因退出 active 就从仓位页消失 | I4 |
| D05 账户隐私/读权限 | 本地/局域网身份验证、CSRF/CORS、日志脱敏、加密连接 | `AuthBoundary` 私有 HTTP/SSE 与公开分开；只读凭据最小权限 | 公开 API 页面绝不能携带账户标识/数量；未授权 401；模拟泄漏测试 | I4 |

## E. 历史验证、运维和配置（实现不可缺的横切需求）

| ID / 功能 | 数据/实现 | 验收/退化 | 切片 |
|---|---|---|---|
| E01 线上线下同公式 | 同一 Go `FeatureEngine`/`ScoreEngine` 接 event replay，存 `modelVersion/inputCutoff` | 同消息回放与 live 结果逐因子一致；任何输入修订产生新快照，不覆写旧决策 | I2–I3 |
| E02 当时可见币池/费用回测 | 按当时挂牌/退市状态、历史费率**当时可见**值、撮合延迟/盘口/手续费和滑点 | 不用当前 top200 回填过去；无完整 OI/资金费历史就缩短样本或剔除该因子 | I3 |
| E03 数据质量/运维 | REST/WS 恢复、消息顺序/完整性、延迟、限流、磁盘/队列监控 | 人为断网/慢盘/错误码/重复消息/缺柱测试；UI 标 stale，报警而不“继续有效” | I1–I4 |
| E04 配置/模拟/权限隔离 | 公共读、私有只读、合成 fixture、无交易 endpoint | 原型离线可跑；实现默认为只读且无订单代码；配置热更新回退测试 | P/I1–I4 |
| E05 规则实验/版本对比 | 可编辑白名单窗口/权重的独立实验版本，配置 diff、成本回测/消融/回放、历史快照 hash | 实验分与默认榜单分分离、标“未验证”；高分但无样本外证据不产生可交易提醒；详见[原型流程](product-flows-and-prototype.md) | I3 |

## 对应用户原话及补充思路

- “每 5s 请求热门 list” → A01–A05 明确可做的 5s 协调、不能用逐合约 K 线轮询的限频、确认窗口的新鲜度。
- “交易量 12h 超 1000w，前 200，动态增删” → A02–A04 给同一时刻、相同单位、上/下阈值与异常处理；**不偷偷加迟滞改变达标定义**，只可延迟取消昂贵订阅并标识延迟。
- “分数最重要、做多/空过滤、多维排序” → A07、B01–B07 区分候选池名额、排序分、风险门控；价涨 OI 涨不自动做空，大量少 OI 不直接推断造假。
- “详情 K 线叠现货、BTC/ETH、大盘、所有 OKX 数据、新闻/解锁/持仓异常” → C01–C09 区分 OKX 原生、链上第三方和可信度，不用一张图误导绝对价格。
- “我的仓位及止盈/止损分析” → D01–D05 做只读账户域、持仓不依赖 active、独立评估和保护性硬风险；真正下单另做授权项目。
