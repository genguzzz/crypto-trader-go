# Go 运行架构、状态机与 API 契约（拟实现）

> 以[功能矩阵](feature-traceability.md)为需求清单；此处定义怎样变成可靠系统。所有 `internal/*` 是**建议目录**，目前尚未创建 Go 代码。OKX 速率、字段和可用端点应在实际 PoC 前按最新[官方文档](https://www.okx.com/docs-v5/en/)复核。

## 1. 模块拓扑和依赖方向

```text
OKX public REST instruments/tickers/candles/OI/funding ──┐
OKX public WS tickers/OI/book/mark ───────────────┐       ├─▶ adapters/okx → normalize / dedupe
OKX business WS candle1m, public WS funding ───────────────┐ │       │                  │
external provider news/unlock/holders ─────────┐│ │       │        ┌─────────▼────────────────────────┐
OKX private REST/WS positions [LATE, READ ONLY] ┐││ │       │        │ market state + event journal     │
                                                │││ │       │        │ as-of, gapfill, source quality  │
                                                │││ │       │        └──────┬───────────────┬──────────┘
                                                │││ │       │               │               │
                                                │││ │       │       720×1m quote ring       │ account private state
                                                │││ │       │               │               │
                                                │││ └───────┼──▶ universe & watchlist       │
                                                ││└─────────┼────────▶ features/score       │
                                                │└──────────┼────────▶ external evidence    │
                                                └───────────┴────────▶ exit assessment ◀───┘
                                                                  │
                                             single-writer SQLite / replay │
                                                                  ▼
                                       HTTP snapshot + SSE delta → Vue pages
```

| Go 建议目录 | 唯一职责和禁止越界 |
|---|---|
| `cmd/server` / `internal/config` | 进程生命周期、显式安全默认值、配置版本/健康检查；不写交易命令。 |
| `internal/okx/public`, `internal/okx/private` | 接口签名/连接/重连/限流/字段解析；SDK 被窄接口包裹，不泄露 SDK 类型到业务层。私有包只暴露读取仓位能力。 |
| `internal/market`, `internal/clock` | 合约目录/现货映射、行情与确认 K 线、`event_ts/received_at/available_at`、乱序/去重/缺口状态。 |
| `internal/universe` | 全池 → 720 根连续 USDT quote 窗口 → 阈值 → Top N；发布原子 snapshot/delta。 |
| `internal/features`, `internal/scoring` | 有版本的因子/入场规则和在线/离线一致性；不访问 HTTP/数据库特定结构。 |
| `internal/portfolio`, `internal/content` | 只读仓位与退出研究；新闻/解锁/链上独立映射、证据与历史版本。 |
| `internal/store`, `internal/replay`, `internal/api` | SQLite 及迁移/保留期；将原始带时间戳事件按序重放；HTTP/SSE/权限与速率。 |
| `web/` | Vue+TS 组件、图表、可访问性/路由/有限内存状态缓存；不在浏览器重新算分。 |

核心窄接口草案（非已实现 API）：

```go
type MarketFeed interface {
    Instruments(ctx context.Context) ([]Instrument, error)
    LatestTickers(ctx context.Context) ([]Ticker, error)
    BackfillBars(ctx context.Context, instID string, from, to time.Time) ([]Candle, error)
    Subscribe(ctx context.Context, channels []Channel) (<-chan MarketEvent, <-chan error)
}
type PositionReader interface {
    Snapshot(ctx context.Context) ([]Position, error)
    Subscribe(ctx context.Context) (<-chan PositionEvent, <-chan error)
}
type FeatureComputer interface { Compute(Snapshot, ModelVersion) (FeatureSet, error) }
type ScoreComputer interface { Score(FeatureSet, PolicyVersion) ScoreCard }
```

**选择窄接口的理由：**SDK 升级和 OKX 变更只影响 adapter；评分版本不依赖某个仓库内部结构；重放 feed 与 live feed 共用下游。失败时可切本项目的原生 HTTP/WS adapter，不迁移领域数据库。具体库见[开源选型](oss-and-data-sources.md)。

## 2. 数据处理时钟：谁在什么时候更新

| 时钟 | 默认目的 | 可变数据/展示语义 |
|---|---|---|
| 全市场 REST ticker ~5s（**可配置**） | 新币提示、行情校验、WS 断开兜底 | `volCcy24h` 仅 24h **基础币**，绝不可替换 12h USDT 榜单；REST 倒退 `ts` 丢弃。[OKX ticker](https://www.okx.com/docs-v5/en/#order-book-trading-market-data-get-tickers) |
| 公共/public/business WS on-event | 当前价格、OI、资金费、K 线持续更新 | ticker 可 100ms、candle 约 1s、OI 更新时约 3s、funding 约 30–90s；这是文档上限/条件，不是稳定事件周期。[WS ticker](https://www.okx.com/docs-v5/en/#order-book-trading-market-data-ws-tickers-channel) · [WS candle](https://www.okx.com/docs-v5/en/#order-book-trading-market-data-ws-candlesticks-channel) |
| confirmed 1m bar arrival | 推进 `turnover12h` 和“确认分” | 仅在 720 连续分钟齐全且共同 `asOfMinute` 下进入/离开 active；缺口暂停声称最新排名。 |
| 5s internal reconcile | 以完整可用 snapshot 比较名单/分数/新增移除，推送 SSE | `checkedAt` 与 `turnoverAsOfMinute` 分开；分数不变也可仅更新 heartbeat/freshness，不滥发重排。 |
| 配置或目录变更 | 强制全量核对所有资格、现货映射/合约面值与持仓 | 指令改变时增 model/config version，向浏览器发 `reset`，不能和旧榜单 delta 混用。 |

### 720 根 K 线的可行性预算

- 2026-09-19 公共快照约 464 个普通 live USDT SWAP；一次 720 根确认 1m，OKX `/market/candles` 一次至多 300，**至少 `ceil(720/300)×464 = 1392` 次**。上限 40 次/2s，即持续打满也要 69.6 秒；按 50% 保守配额约 140 秒 + 网络延迟/分页/重试。**这不是已经测得的耗时或成功率**。[OKX candles](https://www.okx.com/docs-v5/en/#order-book-trading-market-data-get-candlesticks)
- 第一次连接先载入 instruments/全量 tickers，按估计 24h 量排预热优先级（估算不作排名）；held/watchlist 可优先保留独立行情；全部窗口齐备之前 UI 仅显示覆盖率、非“实时精准 Top200”。完成后启动全池 candle1m WS，按流量分片并监测到达率。**若全池 WS 吞吐/订阅不达标，不可同时承诺“5s 精确跨全池名单”**：需二选一，降低覆盖 SLA 或投入更强数据管线，并显式变更 OpenSpec。
- 每轮更新 `confirmed` 只对存在/变化的一分钟计算 `rollingSum += newQuote - expiredQuote`，复杂度按到来的柱数；不每 5s 向 OKX 拉 200×720 根。每 5s ticker 是 `1/5s`，不是 `200/5s`。
- 引入共享 token bucket 按端点/IP 维度限制：ticker 1/5s，OI 全市场定期兜底（WS 优先），funding `instId=ANY` 定期兜底，candles 分页 50% 预算并优先 gap fill；private positions 单一订阅，独立的 User ID 级限制。某端点 429/5xx 时退避＋熔断，不能用无界重试占满全局预算。[OKX rate limits](https://www.okx.com/docs-v5/en/#overview-rate-limits) · [positions](https://www.okx.com/docs-v5/en/#trading-account-rest-api-get-positions)
- WS 建连每 IP 每秒 ≤3；一条连接每小时 subscribe/unsubscribe/login 合计 ≤480 **请求**。批量订阅逐项确认，变更 Top200 时只切可选的 book/trades，**不切全池的 candle1m**。分 `public/business/private` 连接，流量超预算再分片，持久化 ping/pong/reconnect/backoff 计数。[OKX WS](https://www.okx.com/docs-v5/en/#overview-websocket-connect)

## 3. 事件、质量与状态机

```text
EventEnvelope {
  source, channel, instId, instrumentSpecVersion,
  exchangeTs, receivedAt, availableAt, barOpenTs?, confirmed?,
  sequenceOrTradeId?, payloadUnits, payload, sourceVersion
}
ScoreSnapshot {
  seq, asOf, checkedAt, turnoverAsOfMinute, isPreview, modelVersion,
  configVersion, instrument, activeState, entryLong?, entryShort?,
  evidence[], penalties[], confidence, missing[], gate, reasonCodes[]
}
PositionSnapshot {
  accountRefHash, instId, posSide, contracts, contractSpecVersion,
  sourceTs, receivedAt, avgPx?, markPx?, liqPx?, upl?, marginMode?,
  exitAssessment?, quality
}
```

- `exchangeTs`：交易所事件时刻；`receivedAt`：本服务拿到消息；`availableAt`：策略能使用的时刻（至少收到后，外部新闻还需采集/发布时间）；`barOpenTs` 不是该柱结果可用时间。历史 replay 必须遵守 `availableAt<=decisionTime`。
- 同 `(instId, channel, barOpenTs)`：`confirm=0` 可以更新同一盘中柱；`confirm=1` 只能以交易所更晚/更可信版本确定该柱。旧 REST 快照不覆盖更新 WS；同时间戳冲突保留原始证据供审计。不同品种 `asOfMinute` 未对齐时不得互相排名。
- 明确状态 `DISCOVERED → WARMING → LIVE → STALE → RECOVERING → LIVE`；终态 `SUSPENDED/DELISTED`。`WARMING` 历史不够不能进 active；`STALE` 中旧分留屏且灰化标时间，停止新确认分/提醒；`RECOVERING` 补缺失柱＋确认状态，**不是**直接相信重连后第一条最新价就恢复历史；账户失联状态 `POSITION_UNKNOWN`，不等于空仓。
- 有界队列：高频 ticker 可以合并中间状态但保留最后时间戳；**确认 K 线、positions、配置/模型切换不可静默丢弃**，背压/落盘或标缺口并补回。WS books 必须按频道的序列/校验和重建，不用只凭本地到达顺序。
- 指标：`last_exchange_ts/last_receive_ts`, `bar_gap_count`, `universe_coverage_count`, `ws_reconnects`, `rest_429`, `score_latency`, `sse_resync`, `book_stale`, `position_feed_age`, `provider_age`, `disk_free`；UI 系统状态页可见数据质量，不隐藏在日志里。

## 4. 本地数据契约/生命周期

起步可用 SQLite WAL，**先测试** 400+ 路 1m 写入＋评分/UI 并发读取。金额、数量和资金费以十进制字符串/定点整数存储和传输，时间用 UTC 毫秒整数；Go 避免对关键财务数值直接使用二进制 float 比较阈值，指标标准化浮点可留误差声明。

| 表/持久化对象 | 主键与保留 | 作用 |
|---|---|---|
| `instrument_versions` | `(inst_id, effective_at)` | state/spec/ctVal/spot-map 版本；历史回测用当时规则 |
| `candles_1m` | `(market,inst_id,open_ms)`，原始/confirm/quote volume；建议 30–90d 后汇总/导出 | 12h 窗口与 UI、历史 K 线；`confirm=0` 可更新，确认后冻结并留修订审计 |
| `tickers_latest` / `oi_samples` / `funding_samples` | inst/time；ticker latest/有间隔的历史快照，OI/funding 用时序快照 | 全量 ticker 不每 5s 无限存储；OI 需要自己归档才有可验证历史 |
| `universe_snapshots` / `score_snapshots` | `(asof_ms,config_version,model_version,inst_id)` | 保留当时名单、候选分、解释与风险，不以今天的池回填过去 |
| `positions_private` | 加密/脱敏的短保留快照或只在内存；不与公开库表/API 混读 | 仅在只读账户阶段启用，默认不留原始身份信息 |
| `external_events` / `provider_health` | `(provider,id,revision)`、首次可见时间 | 新闻/解锁计划多次修订不能覆盖旧事实 |
| `raw_event_sample` | 可控取样、故障 replay fixture，设容量/保留期 | 审计/差错；不得持久化账户凭据或过度收集个人数据 |

容量数量级（不是实测磁盘大小）：`464×1440≈668,160` 根全池 1m bar/天；30 天约 2,004 万行；`200×1440=288,000` 根活跃集/天。每 5s 全池原始 ticker 若全量落盘会高很多，故只保存需要的 downsample/事件。首次性能门槛建议：公共行情确认柱完整率及 stale 检出可观测、全量预热在 5 分钟内（在配额/网络允许时）、SSE 首屏快照 p95 <2s、确认分更新在完整柱到达后的 p95 <5s；**目标值须实际 PoC 记录样本与机器后调整**，不能视为已满足。磁盘接近阈值时停止历史写入并标系统降级，不让窗口误认为完整。若 SQLite WAL 单写/保留成本超预算，先优化索引/批写/分区归档，再考虑 PostgreSQL/Timescale；不无证据更换全架构。

## 5. HTTP + SSE 契约及 UI 页面

建议单用户先用 HTTP 快照 + **SSE 单向增量**，比直接给浏览器 OKX WS 更易保护密钥、统一过滤与重放；需要浏览器上行交易控制时才评估双向协议。API 路径为蓝图，未上线：

```text
GET /api/v1/health                        公共源/业务/仓位/外部源的 readiness（不含私密内容）
GET /api/v1/config                        可调阈值、版本；敏感字段不得返回
PUT /api/v1/config                        本地已授权、参数校验与版本切换
GET /api/v1/universe?direction=long&sort=score&limit=200&minConfidence=...
GET /api/v1/instruments/{instId}           目录/现货映射、风险标签/质量
GET /api/v1/instruments/{instId}/scores?history=...
GET /api/v1/candles?instId=...&market=swap|spot&bar=1m&from=...&to=...
GET /api/v1/series?instId=...&field=oiCcy|funding|basis|spread...
GET /api/v1/events?instId=...              新闻、公告、解锁和来源/版本
GET /api/v1/positions                      只读、必须授权，未连接不同于空数组
GET /api/v1/positions/{key}/exit           单持仓退出依据和 freshness
GET /api/v1/stream?afterSeq=...            SSE universe/score/quality delta；私有流另行认证
```

例如榜单响应必须带元数据，而非只给一组币：

```json
{
  "seq": 827, "checkedAt": "2026-09-19T12:00:05Z",
  "turnoverAsOfMinute": "2026-09-19T11:59:00Z",
  "coverage": {"eligible": 464, "ready": 455, "warming": 9},
  "modelVersion": "entry-core-v0", "configVersion": 4,
  "sort": "entry_short", "rows": [{
    "instId": "BTC-USDT-SWAP", "turnover12hUSDT": "123456789.01",
    "entryLong": 62, "entryShort": 28,
    "scoreState": "confirmed", "confidence": {"data": 0.98, "validation": "unvalidated"},
    "gate": "PASS", "missing": [], "riskTags": [], "asOf": "..."
  }]
}
```

SSE 每条带单调 `seq`、`snapshotVersion`, `type=add|remove|update|reset|quality|heartbeat`；页面先 GET snapshot，再用 seq 续流，丢帧/重连/服务器缓存过期时 GET 全量再接；从不先订阅后读旧快照导致丢更新。配置修改发 reset，`remove` 带 reason（低于阈值、跌出 Top N、合约状态变更、数据 stale）。服务端保留有限事件 ring；不能补的序号必须 reset。单向公开流不含私有仓位；本地也应绑定 loopback 或显式授权才能在外网暴露账户。

桌面：左侧固定扫描列表（虚拟化、双方向分、状态/排序/自选）、右侧固定选中详情（K 线 + 分解 + 事件）；手机：`/scanner` → `/instrument/:id` 详情，`/positions` 独立路由。详情价格图提供合约原生价＋同币现货**独立轴/系列**，BTC/ETH 与该币比较在**另一张以共同 t0=100 的相对走势子图**；大盘选项为明确标注的“OKX USDT 永续市场篮子”：由前一日已发布的活跃品种中按 quote 成交额选定至多 20 个不同基础币、次日固定等权，按已确认共同时间轴聚合对数收益，成员及权重快照落盘（不是覆盖全加密市场的市值指数），避免绝对价格不等量错觉。OI/fee/news 解锁均需 `source/asOf/status`。仓位账户连接前整个页面显示 not-connected，不能把空数组解释为没有持仓。

## 6. 验证、部署与回滚

- 单机 `go test ./...`（实现阶段）、race detector、官方录制公共响应 fixture、断网/429/乱序/重复/缺柱/上架退市/资金费窗口变化/净与双向持仓合成测试；未来 Android 无关。
- 尽早用真实**公共**接口 PoC 测 200/全池 WS 订阅与补洞、每分钟写入、回放对齐；只读私有接口在安全评审后才测，无交易 key。CI 不能持真实私钥。
- 版本化 schema migration + SQLite WAL 备份/restore 测试；原始资料不可完整回放时系统进入降级，**不**宣称“无缺口”。前端可回滚独立版本，模型权重通过 `modelVersion` 回滚，历史 score 不重写。
- 预期测试矩阵见[功能矩阵](feature-traceability.md)与[评分验证](scoring-and-validation.md)。自动执行如将来需要，是独立系统的审批/熔断与幂等下单设计，绝不通过本 SSE 榜单直接触发。
