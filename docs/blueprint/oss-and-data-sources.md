# GitHub 开源复用、OKX 数据与第三方来源决策

> 调研日 2026-09-19。GitHub 的 `pushed_at`/星数会变、README 支持声明不是集成通过；以下未下载密钥/未实测 200 路或真实仓位。**默认依赖候选 ≠ 已安装依赖。** 许可摘要不构成法律意见，落地时需审阅原始 LICENSE 和NOTICE、锁版本、做安全评估。`gh search repos 'okx trading language:go'`、原 README/仓库 license/官方 API 互证；不把一个“支持 OKX”当作全部 USDT 永续功能可用。

## 1. 框架 vs SDK vs 独立适配器：实际怎么选

| 方案与许可（原仓库） | 可复用到具体功能 / 可证据 | 不能直接依赖的部分/成本 | 决策 |
|---|---|---|---|
| **[okx-go](https://github.com/tigusigalpa/okx-go)** · MIT | README 声明广覆盖 OKX v5 REST、public/private/business WS、重连/模拟环境；适合 A01–A05、B04、D01 的**适配层 PoC**。其自称 335 REST/53 WS 频道仅为作者声明，非本项目验证。 | 社区较小、API 变动时跟进能力未证，账本与评分/限频/窗口需自行实现；测试 README 声明的集成测试需密钥，不能在本次运行 | **首选 PoC 之一**，窄接口封装而非绑定领域对象。 |
| **[amir-the-h/okex](https://github.com/amir-the-h/okex)** · Apache-2.0 | README 列出 OKX 衍生品/资金费/WS 接口及原始事件频道，可与上者对照低层解析/代理/错误码 | README 多处旧 `okex.com` 文档路径；新 API 字段/频道覆盖与重连顺序逐项核实 | **备选 PoC**，只替换 adapter。 |
| **[BanExg](https://github.com/banbox/banexg)** · MIT | 多所 CCXT 风格抽象、swap、OHLCV、funding、book/WS 转储与回放；仓库存在 `okx/` 目录 | README 的已支持交易所列表与代码状态不总一致；抽象单位转换可能隐藏 OKX 原始字段，无法默认保证 720 根 `volCcyQuote` 精度或 WS 正确恢复 | **备选**：若未来要多所且逐字段 PoC 达标时用，不把抽象回放当成熟回测引擎。 |
| **[GoCryptoTrader](https://github.com/thrasher-corp/gocryptotrader)** · MIT | 多所 OKX REST/WS 和回测设计可参考；可对照行情事件/连接管理模式 | README 声明未准备好用于 production，范围过大，直接接入后升级/模块管理成本高；不等于已支持此产品的每个 OKX 口径 | **参考架构**，不作为 MVP 底座。 |
| **[BBGO](https://github.com/c9s/bbgo)** · AGPL-3.0 | 策略/指标/回测及 dashboard；[releases](https://github.com/c9s/bbgo/releases) 出现 OKX futures 更新，值得针对 USDT SWAP 做覆盖 PoC | 框架生命周期强、与个人研究榜单模型不匹配；AGPL/网络提供场景需许可证评审，不直接采纳代码 | **暂不嵌入**，参考策略/控制台模式；未来要执行框架再评审。 |
| **[Banbot](https://github.com/banbox/banbot)** · AGPL-3.0 + 另见商业许可 | README 声称 OKX 合约行情、Web 策略/回测/部署，可做一体化备选 | 核心业务被框架策略/页面模型约束，许可证/商业许可核查，第三方因子和仓位退出逻辑可能需要较重二次开发 | **暂不嵌入**，只在替代整个平台方案时 PoC。 |
| **原生 OKX HTTP/WS adapter** · 本项目 Go 代码 | 精确控制 `volCcyQuote/oiCcy/confirm`、可见时间、全池限流/多频道、原始事件留痕，低框架锁定 | 需要自己处理认证、重连、心跳、分页、时钟、字段变更；代码审计工作量由项目承担 | **与两个 SDK 同时 PoC 的保底方案**；只做产品需用子集，不重写所有 OKX API。 |

**阶段 I1 选择门槛（逐 endpoint 录制、脱敏、可重复的公开 fixture）：**

1. REST 能完整取 `instruments(SWAP/SPOT)`、`tickers(SWAP)`、分页 `candles(1m/spot/swap)`、`open-interest`（全市场与单币）、`funding-rate`，逐字段确认单位、错误码、空值、时间戳。SDK 对应端点**未覆盖**时允许原生 adapter 补齐，但不能静默删除功能。
2. WS public/business 对 200 路及全池 `candle1m`、tickers/OI/funding 的订阅确认、吞吐、乱序、断连重连、重订阅、30s 心跳、代理、限频/队列背压测量；检查 720 窗口不出现未检测缺口。`positions` 暂只用合成 fixture，比对 net/long-short/权限行为；只有 I4 才尝试真实只读。
3. 比较在 Go race 模式下 CPU/内存、恢复后缺口数、429/订阅失败率、依赖树、licence/NOTICE、CI 可重现性；记录版本/commit/最小失败案例。**只有通过才把 SDK 从候选变依赖**。如果两个 SDK 都不达标，转窄原生 adapter，仍保留同一公共接口；不要因为 star 数直接迁移。

## 2. 其他可直接集成的积木

| 用途 | 候选与许可证 | 决策及二次开发边界 |
|---|---|---|
| HTTP / SSE | Go 标准库 `net/http`（Go 版本锁定后评估）或 [chi](https://github.com/go-chi/chi) · MIT | 单用户 API/SSE 优先 stdlib；需要中间件/嵌套路由再用 chi。业务过滤/权限仍归本项目。 |
| WebSocket 底层 | [coder/websocket](https://github.com/coder/websocket) · ISC；[gorilla/websocket](https://github.com/gorilla/websocket) · BSD-2-Clause | 原生 adapter 可选前者；某 SDK 自带 gorilla 不重复集成第二套。必须自行实现 OKX 业务重订阅、速率与 gap fill；WS 包不会解决这些问题。 |
| 金额/数量 | [shopspring/decimal](https://github.com/shopspring/decimal) · LICENSE 文本 MIT | 价格、资金费、仓位张数、成交额使用 decimal/定点数据库编码；指标标准化可以 float 并声明误差。 |
| SQLite/WAL | [modernc.org/sqlite](https://github.com/modernc-org/sqlite) · BSD-3-Clause（无 CGO）或 [mattn/go-sqlite3](https://github.com/mattn/go-sqlite3) · MIT（CGO） | Mac/Linux 单机打包：先两者 benchmark（400+ bar/分钟批写＋浏览器查询），仅择其一；数据库索引、归档、读写边界自研。 |
| Schema migration | [goose](https://github.com/pressly/goose) · LICENSE 文本 MIT | 只处理 DB 迁移；每次迁移必须备份/可回滚测试。 |
| 技术指标 | [go-talib](https://github.com/markcheno/go-talib) · MIT；[cinar/indicator](https://github.com/cinar/indicator) · AGPL-3.0 | 起步只需 EMA/ATR/RSI 等少量指标，可先小规模自研并对标准 fixture；依赖 go-talib 前比对 `warmup`/NaN/确认 K 线。默认不引 AGPL 指标库。 |
| 回测 UI/策略范式 | [Ninjabot](https://github.com/rodrigo-brito/ninjabot) · MIT；上述 BBGO | 参考图表、测试设计；**线上/历史必须运行同一个 Go feature+score package**，不让外部回测框架重写评分语义。 |
| K 线图表 | [TradingView Lightweight Charts](https://github.com/tradingview/lightweight-charts) · Apache-2.0；[KLineChart](https://github.com/klinecharts/KLineChart) · Apache-2.0 | 默认前者：K 线与额外系列/标记/实时更新；对 BTC/ETH 归一化视图另建图而非强行共轴；按原项目 `NOTICE` 留 TradingView 署名/链接；若原型无法表达多轴/移动交互再与后者对比。 |

**复用结论**：先复用低层、可审计积木（WS、decimal、SQLite、图表）与待测 SDK；`Universe`, `FeatureEngine`, `ScoreEngine`, `ExitAssessment`, 时序/数据质量和 API 聚合是本工具的竞争性业务逻辑，自己实现并测试，不能将大型框架当作可替代的黑箱。

## 3. OKX 数据 API 覆盖：字段/历史/不能保证的事

| UI/算法想要的数据 | OKX 第一手入口 | 现实约束与降级 |
|---|---|---|
| 合约/现货标识、合约面值、状态 | [instruments](https://www.okx.com/docs-v5/en/#public-data-rest-api-get-instruments)；[instruments WS](https://www.okx.com/docs-v5/en/#public-data-websocket-instruments-channel) | token 同名不能靠 ticker 猜链上地址；合约面值变化须用版本表。 |
| K 线/成交额 | [最新 K 线](https://www.okx.com/docs-v5/en/#order-book-trading-market-data-get-candlesticks)；[历史 K 线](https://www.okx.com/docs-v5/en/#order-book-trading-market-data-get-candlesticks-history)；[WS K 线](https://www.okx.com/docs-v5/en/#order-book-trading-market-data-ws-candlesticks-channel) | 最新最多 300 根/次、40/2s；历史接口 20/2s、最多 300 根/页。永续 `vol` 张数，`volCcyQuote` USDT；缺口不能当零。 |
| 盘口/深度/价差 | [REST book](https://www.okx.com/docs-v5/en/#order-book-trading-market-data-get-order-book)、[WS book](https://www.okx.com/docs-v5/en/#order-book-trading-market-data-ws-order-book-channel) | L2 按需订阅；快照/增量及序列校验；历史订单簿文件可有 T+3 发布/覆盖不全，不能伪造历史滑点。 |
| mark/index 与风险 | [mark](https://www.okx.com/docs-v5/en/#public-data-rest-api-get-mark-price)、[index](https://www.okx.com/docs-v5/en/#public-data-rest-api-get-index-tickers) 和各自 candles | mark/index K 线是价格而非成交量；指数 `BTC-USD` 不等于 `BTC-USDT-SWAP`。 |
| 持仓量/OI 历史 | [当前 OI](https://www.okx.com/docs-v5/en/#public-data-rest-api-get-open-interest)、[逐合约 OI 历史](https://www.okx.com/docs-v5/en/#trading-statistics-rest-api-get-contract-open-interest-history) | OI 有 `oi` 张数、`oiCcy` 基础币和 `oiUsd`；历史端点每页/总条数与可追溯区间需实测，不能承诺可任意回补 1m OI；本地持续保存已知快照。 |
| 资金费率 | [预估/结算](https://www.okx.com/docs-v5/en/#public-data-rest-api-get-funding-rate)、[历史](https://www.okx.com/docs-v5/en/#public-data-rest-api-get-funding-rate-history) | 在线 history 约近 3 个月；更早文件覆盖需核对。预测费率 vs 最终费率按**当时可见**分开保存，不假设每 8h。[历史文件](https://www.okx.com/docs-v5/en/#public-data-rest-api-get-historical-market-data) |
| 多空账户比/主动成交 | [trading statistics](https://www.okx.com/docs-v5/en/#trading-statistics-rest-api)、[trades-all](https://www.okx.com/docs-v5/en/#order-book-trading-market-data-ws-all-trades-channel) | 账户比不是市场净头寸；逐笔抓全部全池成本高，若仅采高分币只做详情标签，不能在默认榜单区别打分。 |
| “我的仓位”和账户风险 | [positions](https://www.okx.com/docs-v5/en/#trading-account-rest-api-get-positions)、[balance](https://www.okx.com/docs-v5/en/#trading-account-rest-api-get-balance)、[account-position-risk](https://www.okx.com/docs-v5/en/#trading-account-rest-api-get-account-and-position-risk) | 全仓/组合保证金有交叉风险；[risk-state](https://www.okx.com/docs-v5/en/#trading-account-rest-api-get-account-risk-state) 有账户模式限制，空数据不能当零风险。仅 I4 只读最小权限。 |
| OKX 平台公告 | [announcements](https://www.okx.com/docs-v5/en/#announcement-get-announcements) | 公告可能约 5 分钟延迟，不是所有项目新闻；原型可用合成数据，I4 可作为第一源。 |

## 4. 外部供应商与 token-chain 映射的真实边界

| 功能/候选来源 | 能提供什么/不能提供什么 | 技术与采购决策 |
|---|---|---|
| [CoinGecko coin metadata](https://docs.coingecko.com/reference/coins-id) | circulating/total/max/FDV、链上地址映射线索；**没有因此可靠预测未来解锁日**；[News API](https://docs.coingecko.com/reference/news) 为付费权限且历史/分页有限，价格以[官网当前页面](https://www.coingecko.com/en/api/pricing)为准 | 先用作映射核验/资料卡，不因数据存在就加入实盘方向分；订阅前审查配额、历史、许可。 |
| [Tokenomist API](https://docs.tokenomist.ai/api-documents/introduction) | 解锁/线性释放/代币分配计划，可能与项目实际链上转账不同 | 可选 I4；[价格/额度](https://tokenomist.ai/pricing)与文档对调用量有不同表述，采购前书面确认额度、历史修订、商用/二次展示权限和 symbol 覆盖。无供应商时显示“未知”，不是无解锁。 |
| [OKX OnchainOS holder API](https://web3.okx.com/onchainos/dev-docs/market/market-token-holder)／[top holders](https://web3.okx.com/onchainos/dev-docs/market/market-token-cluster-top-holders) | 链上地址/群组信息；**与 OKX 交易所私有仓位/真实受益人不同**，holder list 的排序不一定按余额，不能直接拿前 100 行当 Top100 持仓集中度 | 独立于 OKX v5 市场 API，需审[权限/费用](https://web3.okx.com/onchainos/dev-docs/market/market-api-fee)、链支持、地址归因；不可得时只打 `holder-data-unknown` tag。 |
| [Etherscan holders](https://docs.etherscan.io/api-reference/endpoint/tokenholderlist) | 链上地址余额，需 PRO 权限且仅覆盖其支持链/合约 | 与 OnchainOS 比较返回顺序、托管地址识别、分页/费用；合约地址过时/同名误配必须人工覆核。 |
| 项目方文档/公告与经验证新闻源 | 事件源、首次发布时间、计划修订/生效时间 | 核对项目官方域/合约地址/UTC 时区，存 revision；聚合转载去重，新闻“提到 BTC”不必然属于某小币。没有可复现历史就只展示，不用于回测。 |

**映射主键不能只用 ticker：**`instId → projectId → chainId + tokenContractAddress + validFrom/validTo + mappingConfidence + reviewedBy`；跨链包装币、代币迁移/重发行、同名 ticker、预市场合约都可能造成误警示。合约的 OKX 现货“持有分布异常”若指交易所账户持有人分布，**公开 OKX API 没有可核实的全体账户级分布**；链上地址集中只能标“链上供应集中风险”，不能叫“OKX 现货仓位集中”。默认外部数据失败只影响相应资料卡/标签，**不修改核心分数**。

## 5. 采购/依赖判定顺序

1. I1 只需 OKX 公共源/Go adapter/图表；I2 使用自身保存的 OI、价格、funding。费用为公开接口的网络/存储成本；外部付费方案均非 MVP 前置依赖。
2. I4 新闻先用有来源的 OKX 公告；第三方解锁/holder 要先用样本验项目覆盖率、版本/发布时间、已知链映射、月配额和预算，再买/集成。
3. 所有供应商都保留 `provider/sourceTimestamp/receivedAt/coverage/limit/license`；超配额、撤稿、修订、停用时展示未知并保留历史已知版本，不把供应商失效变成“安全/无事件”。
