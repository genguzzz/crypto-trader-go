# 架构与目录

## 1. 总体：前后端分离 + 单一数据库

```
            OKX v5 公共接口（无 Key）
   REST（限频令牌桶）          WebSocket
   合约/行情/K线/历史 OI/资金费   business: candle1m 全池
                               public: OI、资金费全池；tickers 仅活跃池+自选
              │                        │
              ▼                        ▼
   ┌──────────── 后端 Go（cmd/server）──────────────────────┐
   │ internal/okx      适配层：限频、重试、重连、订阅差分、代理/DoH │
   │ internal/market   引擎：录制 → 补洞 → 每分钟监控池 → 事实面板  │
   │                   → 规则评分 → 每 10 秒盘中重算 → 整点快照   │
   │                   → 14 天回放 → 7 天 1m 历史回补            │
   │ internal/features 事实面板公式（实时/回放共用）              │
   │ internal/rules    规则定义、校验、打分、预置对照规则         │
   │ internal/track    战绩统计（Rank IC、t、分位收益、成本）     │
   │ internal/store    SQLite（WAL）+ database/sql 连接池         │
   │ internal/api      REST JSON + SSE 推送 + 图标代理 + 可选登录  │
   └──────────────┬───────────────────────────┬──────────────┘
                  │ /api/v1/*（JSON / SSE）     │
                  ▼                           ▼
   前端 web/（Vue 3 + TS + Vite + Lightweight Charts）   SQLite 文件 data/ctg.db
   开发：Vite dev server 代理 /api；生产：前端内嵌二进制，Docker 运行（见 §7）
```

- **分离方式**：后端只暴露 `/api/v1/*`；前端是独立 Vite 工程。为了本地使用方便，构建产物可 `go:embed` 进二进制（`-web=true` 默认）；`-web=false` 时后端只提供 API，前端可由 Vite 开发服务器或（将来）nginx 容器单独提供。
- **数据库与连接池**：SQLite WAL 单文件，`database/sql` 连接池（`SetMaxOpenConns(4)`，`busy_timeout` 10s）；写入由单个 writer goroutine 每 2 秒批量提交，读写不互相阻塞。热数据（最近 1500 分钟 1m K 线）放内存环形缓冲。若日后多实例/大数据量，`internal/store` 是唯一需要换成 PostgreSQL 的地方。

## 2. 数据流与时钟

| 时钟 | 做什么 |
|---|---|
| WS 推送（~0.1–7s） | 更新盘中 K 线、最新价、OI、资金费 |
| 每个确认分钟（收齐或最迟 +65s） | 12h 窗口 → 监控池增删（带原因）→ 事实面板 → 规则评分 → SSE `universe` |
| 每 10 秒 | 用未收盘分钟重算活跃池/自选的事实面板与分数（`preview`，会变动） |
| 每 2 秒 | SSE `prices` 推送最新价 |
| 每个整点 | 保存每条规则对活跃池的打分快照（不可改写，用于战绩） |
| 15s / 30s / 5min | 现货行情、永续 ticker 兜底、合约目录刷新 |
| 启动 / 改设置 | 5m 快速建池 → 1m/1H 回填 → 14 天小时级回放 → 7 天 1m 历史回补 |

## 3. 关键算法（摘要）

- **监控池**：连续 720 根已确认 1m K 线 `volCcyQuote` 之和 > 阈值（默认 500 万 USDT），按成交额降序取前 N（默认 200）。缺分钟≠0 成交（OKX 对无成交分钟也发 0 量 K 线），缺口标 `stale` 并按“最早缺失分钟”补洞。
- **事实面板**：1h/4h/24h 收益、相对 BTC 4h、4h 趋势强度（按 48h 小时波动标准化）、币本位 OI 变化、放量倍数（对 7 天中位）、资金费/小时（4h/8h 换算）、基差、点差。缺失即未知。
- **规则评分**：字段在活跃池内的分位 → 映射到 ±1 → 按权重加权平均 → `50 + 50 × Σwx / Σ|w|`。多/空独立；任一输入缺失则未评分；点差门槛独立。
- **战绩**：同一时点池内“分数排名 vs 未来 h 小时收益排名”的 Spearman Rank IC，周期内不重叠抽样；t 值；前 20% 超额与扣双边 5bps taker + 点差后的净收益。

详见 [蓝图修订](blueprint/revision-2026-09-26.md) §2。

## 4. 技术选型

| 选择 | 理由 |
|---|---|
| Go 1.25 | 长连接/并发、静态单二进制（无 CGO），交叉编译即可部署 |
| 自研 OKX 窄适配（`net/http` + gorilla/websocket） | 只需约 8 个 REST + 5 个 WS 频道；精确控制单位/时间戳；实测单连接订阅全池无错误 |
| SQLite（modernc，纯 Go）+ 连接池 | 实测每分钟全池写入 ~12ms、3 天 K 线查询 ~4ms；无需独立数据库服务 |
| HTTP + SSE | 单向推送足够；客户端每次事件重新拉快照，天然自愈 |
| Vue 3 + TypeScript + Vite | 类型安全、构建快；Lightweight Charts 画 K 线（Apache-2.0，保留署名） |

## 5. 目录结构

```
cmd/server/            入口：参数、网络（代理/DoH）、启动引擎与 HTTP
internal/okx/          OKX REST/WS 适配、限频、拨号
internal/store/        SQLite 表结构与读写
internal/market/       引擎：engine.go（启动/录制/WS）、universe.go（每分钟池）、
                       preview.go（10 秒盘中）、rules.go（规则/回放/战绩）、
                       history.go（7 天 1m 回补）、settings.go（运行时设置）、
                       queries.go（图表/详情/状态查询）、state.go（内存结构与 Series）
internal/features/     事实面板
internal/rules/        规则与打分
internal/track/        战绩统计
internal/api/          HTTP 路由、SSE、图标代理、Basic 登录
web/                   前端工程（src/views 页面，src/components 组件，embed.go 内嵌产物）
deploy/                docker-compose.yml（服务器运行定义）、deploy.sh（构建/上传/替换/回滚）
research/poc/          调研探针（独立 Go module，不是生产代码）
docs/                  文档；docs/blueprint 为方案与修订
openspec/              需求与变更记录
```

## 6. API（/api/v1）

`GET health|status|meta|settings|universe?scope=active|all|watch|instruments/{id}|candles|compare|series|rules|rules/{id}/track|icons/{ccy}.png|stream(SSE)`；
`PUT settings|watchlist/{id}`，`POST rules`，`DELETE rules/{id}|watchlist/{id}`；`GET positions` 固定返回未连接（I4 前）。

## 7. 部署形态

```
本机 deploy/deploy.sh ──scp 静态二进制──▶ old-server:/opt/ctg/bin/ctg ──(bind mount, ro)──▶ 容器 ctg
                                                                  distroless/static:nonroot (2 MB)
公网 443 ─▶ nginx stream（SNI 默认路由）─▶ 127.0.0.1:8444 gateway-ctg.conf（TLS *.gugenzzz.top）─▶ 127.0.0.1:8686
```

镜像不含业务代码，升级只替换二进制并重启容器；数据在 `/opt/ctg/data`（SQLite），与二进制版本解耦。细节见 [运行与部署](operations.md)。
