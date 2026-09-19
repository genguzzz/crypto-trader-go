# 个人 OKX 合约交易研究工具：Go 优先方案（阶段 1）

- 日期：2026-09-19（Asia/Shanghai）
- 目标仓库：`/Users/gugen/Code/crypto-trader-go`
- 状态：**研究与技术决策，不是原型、已经验证的赚钱策略或交易系统**。阶段 2 做交互原型，阶段 3 再实现。
- 历史参考：旧仓库 `/Users/gugen/Code/cryptoBot/docs/okx-contract-platform-technical-research-2026-09-12.md`（Top100/24h/Python）。旧仓库已弃用，不是此仓库的代码基础；本仓库只迁入本次新目标 **Go + Top200 + 滚动 12h + 独立多空/仓位分析** 的调研文档。

## 1. 推荐路线及理由

**推荐 Go 模块化单体 + Vue/TypeScript 前端 + 官方 OKX v5 接口的独立适配层。** 首页为前 200 USDT 永续候选集，后台按秒级 WS 更新行情，约每 5 秒协调/发布名单；每个合约有独立 `long_entry` 与 `short_entry` 机会分，按规则分默认排序，但允许按其他指标排序、按方向和风险标签过滤。独立仓位页后续只读账户数据，提供 `long_exit` / `short_exit` 风险与离场依据。分数是**研究候选质量**，不是预测概率，更不自动触发订单。

Go 适于维持多路 WebSocket、限流、背压与单一评分真源；旧仓库的 Python/Freqtrade 至多作研究对照，不计划代码迁移，**不把两套不同公式同时用于线上和回测**。前端可参考旧仓库的 Vue 3 和 Lightweight Charts 经验，但须在新仓库独立实现。个人单机 MVP 可先实测 SQLite WAL 的吞吐与保留期；若全量 1m/OI 长期存储、并发查询或多实例超过预算，再上 PostgreSQL/TimescaleDB，而不是初版引入 Redis、微服务和两套交易引擎。以上是工程推荐，尚未对 Go SDK、存储写入、WS 200+ 订阅做压测。

### 与旧方案的关键差异

| 维度 | 2026-09-12 历史研究 | 现阶段决策 |
|---|---|---|
| 主实现 | Python、Freqtrade + FastAPI | 全新 Go 项目；旧仓库不再开发 |
| 候选集 | Top100 / 24h 估算 USDT 成交额 | Top200 / 滚动 12h `volCcyQuote`，严格 >1000 万 USDT |
| 行情 | REST 降级 + WS 目标 | WS 实时 + REST 初始化/补洞；本地每约 5s 名单核对 |
| 评分 | 四方向建议 | 双方向入场和**独立仓位离场**研究及验证协议 |
| 交付 | 旧探针/监控修复 | 本次只研究；原型和 Go 实现分两次 OpenSpec 变更 |

## 2. 候选池与实时性：必须先定义清楚

今天的**一次无凭据公共接口**核对：OKX `/public/instruments?instType=SWAP` 共 482 个 SWAP，其中 `state=live`、`settleCcy=USDT`、`*-USDT-SWAP` 为 467 个，内有 3 个 `ruleType=pre_market`。数量是 2026-09-19 快照，之后会变；默认排除预市场，剩余当时为 464 个，实际达标数可能不足 200。[OKX instruments](https://www.okx.com/docs-v5/en/#public-data-rest-api-get-instruments)

**默认定义：**

```text
eligible = state=live AND instId endsWith "-USDT-SWAP"
           AND settleCcy=USDT AND ruleType!=pre_market
rolling_turnover_12h_usdt = sum(720 continuous confirmed 1m candles.volCcyQuote ending at a shared as-of minute)
active = first 200 eligible instruments sorted by rolling_turnover_12h_usdt DESC
         where rolling_turnover_12h_usdt > 10_000_000
# 同成交额按 instId 排序；存在缺口、时间错位或没有完整 720 分钟数据则标 warming/stale，不假装满足过滤。
```

`volCcy24h` 在 SWAP ticker 中是**基础币数量**，`vol24h` 是合约张数，都不是 12h USDT 成交额；`volCcy24h * last` 只是粗略的 **24h** 当前价估计，最多用于候选发现。`bar=12H` 是固定对齐的 12 小时柱，并非任意时刻向前滑动的窗口。`1m` K 线 `volCcyQuote` 是计价币成交额；在 USDT 合约上即 USDT，`confirm=1` 才是已确认柱。每 5 秒**核对名单/价格并不意味着 12h 成交额每秒精确变动**：用 720 根确认柱，边界最大约一分钟误差；盘中柱可另示“临时估计”，不能与确认值混算。若一定要过去 43,200 秒逐笔精确，则需按 trades-all 成交时间/合约面值维护全量流水和缺口，负载与回填成本显著增加，不作为 MVP 默认。[OKX tickers](https://www.okx.com/docs-v5/en/#order-book-trading-market-data-get-tickers) · [candlesticks](https://www.okx.com/docs-v5/en/#order-book-trading-market-data-get-candlesticks) · [all trades](https://www.okx.com/docs-v5/en/#order-book-trading-market-data-ws-all-trades-channel)

**刷新/增删：**启动 REST instruments → 约 464 个合约按限流队列回填 720 根 1m 历史（单次至多 300 根，至少约 1392 次，打满 40 次/2 秒理论下界近 70 秒，实际更长）；期间显示“预热”与覆盖率，而非错误的榜单。公共 WS `candle1m` 持续更新（核实 business 路径），ticker/可用时 OI 与 funding 同步；约 5 秒做原子快照、重算阈值/前 200，比较集合差异 `added/removed/changed` 推送 UI。REST 全量 ticker 可每 5s 获取新上市/初筛/校验，历史重试按配额错峰；上市状态也可接 instruments WS。阈值变动、下架、stale 数据或断连时必须标明原因，绝不能悄悄用更旧快照覆盖较新数据；只有前 200 的**昂贵深度/逐笔**订阅可按需增删，全池的低速 K 线统计不能因移出前 200 就停止，否则永远不能正确重入。对于掉到阈值外的币，UI 保留短暂 `removed` 事件而不是直接消失。[OKX WS/connect](https://www.okx.com/docs-v5/en/#overview-websocket-connect) · [market data notes](https://www.okx.com/docs-v5/en/#order-book-trading-market-data)

**限频关键算术：**200 个合约逐个每 5s 查 K 线 = 80 次/2 秒，已超过 `/market/candles` 40 次/2 秒 IP 限制；全量 ticker 1 次/5s、全市场 OI 一次、全市场 funding 一次则量级不同。WS tickers 最快约 100ms、有数据才推；candle 最快约 1 秒；OI 更新时约 3 秒，funding 30–90 秒，**评分每 5s 重算不保证每个因子都在 5s 内刷新**。WS 每 IP 每秒最多 3 次建连、单连接每小时最多 480 次 subscribe/unsubscribe/login **请求**；按频道分片、批量订阅、检查订阅确认、有界队列/重连退避/30s ping 保活。不要把私有 positions 某些频道“最多 30 个连接”误读为“连接只支持 30 币”。[OKX rates](https://www.okx.com/docs-v5/en/#overview-rate-limits) · [WS connect](https://www.okx.com/docs-v5/en/#overview-websocket-connect) · [candles](https://www.okx.com/docs-v5/en/#order-book-trading-market-data-get-candlesticks) · [open interest](https://www.okx.com/docs-v5/en/#public-data-websocket-open-interest-channel) · [funding](https://www.okx.com/docs-v5/en/#public-data-websocket-funding-rate-channel)

**必留的标准化字段：**`instId, base/quote/settle, source/channel, exchangeTs, receivedTs, confirmed, units, missing/stale, mappingVersion, scoreVersion`。OI `oi` 为张数、`oiCcy` 为标的币数量、`oiUsd` 为美元名义额；做“真实加仓”候选因子时优先比较 `oiCcy`（或正确按合约规格换算后的币数量），**不能仅因为价格抬升导致 `oiUsd` 上升就断定新增仓位**。资金费率记录预估值、已结算值及下次结算时间，不能假设恒定 8h 周期。`/account/positions` 返回的 `pos` 对 SWAP 也是张数，需对齐 `posSide` 净持仓/双向持仓。私有 API 留到阶段 3，只读 key、IP 限制、隔离和审计先评审。[OKX OI](https://www.okx.com/docs-v5/en/#public-data-rest-api-get-open-interest) · [funding](https://www.okx.com/docs-v5/en/#public-data-rest-api-get-funding-rate) · [positions](https://www.okx.com/docs-v5/en/#trading-account-rest-api-get-positions)

## 3. 数据能拿到什么、拿不到什么

| 模块 | 第一手来源与字段 | 边界及界面处理 |
|---|---|---|
| 合约/现货 K 线、mark/index、成交量 | OKX market/public REST & WS | `BTC-USDT-SWAP` ↔ `BTC-USDT` 映射需校验是否挂牌；无现货就标 unavailable。基础资产基准 BTC/ETH 可选。[OKX market](https://www.okx.com/docs-v5/en/#order-book-trading-market-data) |
| 盘口、价差、OI、预测/历史资金费率 | OKX 公开接口；OI 历史覆盖需验证 | 它们是不同频率的观测；成交量本身**不表示**主动买盘或卖盘。[OKX public data](https://www.okx.com/docs-v5/en/#public-data-rest-api-get-open-interest) |
| 仓位、浮盈亏、仓位方向、保证金/强平价 | OKX 私有账户/positions（后续只读） | 只有授权后才有“我的”；模拟持仓和真实持仓不可混淆。[OKX positions](https://www.okx.com/docs-v5/en/#trading-account-rest-api-get-positions) |
| 新闻、项目解锁、现货地址集中度 | 项目披露/可靠新闻与链上索引、第三方 API | **不是 OKX 原生完整数据**；保留发布时间、来源、token-chain 映射、可信度。交易所/桥/锁仓地址不可直接算“巨鲸”，币种缺映射就标未知。[Binance Research 供给风险背景](https://www.binance.com/en/research/analysis/low-float-and-high-fdv-how-did-we-get-here) |

## 4. GitHub 开源调研与选型

通过 `gh search repos 'okx trading language:go'`、`gh search repos 'crypto trading bot language:go'` 及原仓库 README/许可交叉检查（2026-09-19）；近期有提交**不代表适合个人平台或通过 OKX 永续实测**。

| 仓库 | 许可/观察 | 本项目判断 |
|---|---|---|
| [GoCryptoTrader](https://github.com/thrasher-corp/gocryptotrader) | MIT；多所行情、OKX REST/WS 与回测相关模块；README 明言尚未 production-ready | 可研究 OKX adapter / 回测事件模型，不直接把完整引擎装进个人 MVP。须测合约字段与恢复语义。 |
| [BBGO](https://github.com/c9s/bbgo) | AGPL-3.0；Go 指标/回测/多所/仪表盘，README 明列 OKX 现货，近期 [releases](https://github.com/c9s/bbgo/releases) 也列 OKX futures 订单/频道更新 | 说明不再只是现货；仍须实测 USDT 永续 200 路行情、OI/资金费和恢复能力；许可证有集成风险，不作默认内嵌依赖。 |
| [Banbot](https://github.com/banbox/banbot) / [BanExg](https://github.com/banbox/banexg) | Banbot AGPL-3.0/商业许可，README 声称 OKX 现货及合约 WS；BanExg MIT、仓库含 `okx/`，文档列表与代码不完全一致 | 若未来要一体化回测/执行再 PoC Banbot；BanExg 可测试单个 adapter，但不能把回放等同完整可用回测。 |
| [okx-go](https://github.com/tigusigalpa/okx-go) / [amir-the-h/okex](https://github.com/amir-the-h/okex) | Go SDK 候选；前者 MIT、含 REST/公私 WS/模拟模式，社区较小；后者为 gh 搜索结果需另审代码和许可 | 实现阶段先做最小 PoC 比签名、分页、限流、断线重订阅、合约测试覆盖；锁定版本。保留原生 HTTP/WS 适配备选。 |
| [Lightweight Charts](https://github.com/tradingview/lightweight-charts) / [KLineChart](https://github.com/klinecharts/KLineChart) | 两者 Apache-2.0，前者已有仓库前端依赖；前者还要求 NOTICE 中的署名/链接 | 先沿用 Lightweight Charts 做叠加/比较，原型若绘图需求不足再对比 KLineChart；它们不是行情或评分引擎。 |

**选型结论：**不“重写全套交易所 API 规范”，也不因为框架自称支持 OKX 就接入执行链。Go SDK/框架先跑 **公共 SWAP 200 路 WS、断连、恢复、12h quote 成交额一致性** PoC，比较稳定性；业务评分与安全控制始终自己持有。AGPL 的再分发/网络使用影响需结合实际使用场景做许可核查，不能仅凭个人项目就假定无约束。

## 5. 规则评分的可证伪初版（不是投资建议）

**先过滤，再评分，再显示质量。** 硬门槛：可交易 live、12h USDT 成交额、关键数据完整/时间新鲜、品种映射可信、在既定名义下点差/预估滑点/费用不超预算。缺失关键项返回 `unavailable`，不是 0 分；缺失可选项置中性并降低独立 `confidence`。榜单全方向默认按 `max(long_entry, short_entry)` 排序，单方向筛选按对应方向分，分相同时按 12h 成交额和合约 ID。展示“观测值—变换后的因子—对子分影响—数据源—更新时间—模型版本”。

设 `d=+1` 做多，`d=-1` 做空；价格标准化动量 `P`、现货**主动买卖方向** `S`、永续主动买卖方向 `D` 及 `O=sign(P)×(标的币 OI 增长强度)×(现货参与度)` 均限于 `[-1,1]`。**没有主动方向数据时 `S/D` 缺失，不可拿总成交额替代。** 一个待验证的初始公式：

```text
entry[d] = clamp(50 + 50*d*(0.35*P + 0.25*S + 0.25*O + 0.15*D)
                     - 20*(0.25*volumeOiMismatch[d] + 0.25*crowding[d]
                           + 0.20*carryCost[d] + 0.15*eventRisk[d]
                           + 0.15*concentrationRisk[d]), 0, 100)
confidence = dataCoverage × sourceFreshness × historicalStability  # 未验证时明确标注
```

权重/系数只是**预注册待检验的原型参数**，非命中率。价格上涨、以币计量 OI 扩张且现货跟进 → 多头证据增强、做空分承压，但**不是绝对不能做空**；放量大涨而 OI 不跟随、尤其只是合约放量而现货参与不足 → 对“合约趋势确认”贡献不足并产生 `volumeOiMismatch` 软惩罚，不能据此认定有人“造假”。无现货主动方向数据时该惩罚不得凭空计算。资金费率/基差过热可施加方向性软惩罚，预期需支付资金费是成本；已核实解锁与地址集中度先做风险 tag，未经验证不让“将解锁”自动给空头加分。高相关因子按类别限总权重；后续乘法只做硬质量门控，而非任意把若干信号相乘放大。OI 是**多空共同未平仓量**，OI 增加并不揭示哪边是“庄家”，解锁日期也不等于当天会抛售。[CME OI 定义](https://www.cmegroup.com/education/lessons/open-interest) · [Binance Research 供给风险](https://www.binance.com/en/research/analysis/low-float-and-high-fdv-how-did-we-get-here)

**仓位离场**单独建 `exit_long`/`exit_short`（而非 `100-entry`）：趋势失效、OI/现货支持衰减、价差和波动恶化、资金费持有成本、距强平的安全边际、已有止损/止盈和持仓期限。事件/流动性/强平风险是独立硬警告；展示分数与理由，不替用户执行止盈止损，也不暴露账户明细日志。

**验证门槛：**先决定信号的预测/持有期限（如 4h）和不可调的目标标签；存储当时可见的价、OI、预估费率、解锁公告版本、上市/退市状态；多空分别按成交延迟、双边手续费、冲击/滑点、资金费成本回测。按时间滚动样本外验证，重叠标签 purge/embargo，保留一次性最终测试区间；分别与仅价格趋势/不含 OI/不含现货/不含事件的消融对比，记录所有试过的参数避免挑最漂亮的历史结果。股票订单流研究不自动转化为加密货币可交易 alpha；任何具体分数阈值须先验证再上线提醒。[订单流论文（对照、非本市场证明）](https://arxiv.org/abs/1011.6402) · [回测过拟合研究](https://papers.ssrn.com/sol3/papers.cfm?abstract_id=2326253)

## 6. 产品原型需求（阶段 2，不在本阶段实现）

**桌面（≥大屏）：**左侧筛选/排序栏＋可虚拟滚动榜单（合约、方向、两个入场分/排名、12h 成交额、OI 变化、费率、新鲜度、异常 tag）；右侧顶部合约状态/分数分解，主体双图层：本币合约 K 线与可选现货 K 线，另设锚定到同一时刻=100 的 BTC/ETH/现货/合约收益对比视图，不能用不同量级的绝对价格重叠误导；下部盘口、资金费/OI/基差、新闻/解锁证据与来源。成交额也能排序，但分数始终默认主排序。用户选中币种不因列表重排而跳动。

**手机：**`/scanner` 列表（方向 tab/过滤 chip/紧凑分数和风险标记）→ `/instrument/:id` 独立详情（标签页行情、分数、事件）；`/positions` 独立只读页面，点开才看 `/positions/:id` 离场依据。原型用合成数据覆盖新增、移除、分数变化、长/短过滤、同资产现货缺失、新闻/解锁无来源、数据变旧、WS 断线和账户未连接；不得用真实资产或权限。阶段 2 验收：桌面和窄屏可点击主流程、标签来源清楚、图例/标准化对比不误读、刷新及预热/错误态可见。设计完成评审后才冻结实现数据契约。

## 7. 后续阶段、验收门槛和待决定事项

1. **阶段 1 调研（本次）：**官方文档、公共接口快照、GitHub 方案、口径/负载/评分假设、OpenSpec/项目提示；验证文档和来源。**不触及账户和下单。**
2. **阶段 2 原型（新 OpenSpec change）：**固定合成样本、桌面/手机交互、可视化与四种数据状态，用户评审信息密度和分数解释；无私有 API。
3. **阶段 3 实现（新 change、分切片）：**先公共 WS/REST + 回填/时间质量与性能压测（可重放/故障注入）→ universe/评分及历史验证 → API/UI → **审批后**只读仓位与离场分析。下单/自动止损若未来有需求，**再次**单独提案，先 dry-run、权限隔离和故障风控，不属于这三阶段当前承诺。

**待确认，不阻塞阶段 1：**“热门榜”是否就是 12h 成交额前 200（默认）还是另一官方榜单？5s 是否要求逐秒精确成交额？默认仅常规 `live` USDT 永续并排除预市场，对吗？评分关注 1m、15m、4h 哪个持有期？新闻/解锁优先哪个供应商及预算？这些都应在原型评审或实现前形成显式 OpenSpec 决策。
