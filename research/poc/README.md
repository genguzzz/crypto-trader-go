# 调研 PoC 探针（2026-09-26）

用于证伪/验证[蓝图](../../docs/blueprint/README.md)关键假设的**一次性研究程序**，只访问 OKX 公共接口，无凭据、无私有端点、无下单。不是阶段 3 的生产代码；实现阶段可参考但不直接复用（生产 adapter 另建于仓库根 Go module）。

结论与解读见 [蓝图修订 2026-09-26](../../docs/blueprint/revision-2026-09-26.md)。

| 程序 | 验证什么 | 运行（在本目录） | 耗时 |
|---|---|---|---|
| `cmd/turnover` | 全池 720×1m 精确 12h USDT 成交额、预热请求数/时长、>1000 万达标数量、5m/1H/24h ticker 近似是否选出同一名单、分钟缺口 | `go run ./cmd/turnover -rate 20 -out results/turnover-<ts>.json` | ~4 min（50% 配额） |
| `cmd/wsprobe` | 单连接订阅全池 `candle1m`(business) / `tickers` / `open-interest` / `funding-rate`(public)：订阅确认、每分钟 confirm 到达率与延迟、各频道消息量/间隔 | `go run ./cmd/wsprobe -d 4m30s -out results/ws-<ts>.json` | 指定时长 |
| `cmd/factorstudy` | 近 ~51 天 1H 数据：时点币池规模（按星期）、蓝图 `entry-core-v0` 及各因子对未来 H 小时收益的横截面 Rank IC、分位多空价差（含成本） | `go run ./cmd/factorstudy -h 4 -out results/factorstudy-h4.json`；复用缓存 `-end 20260926T15` | 首次 ~35 min（rubik OI 限频 5/2s），缓存后秒级 |
| `cmd/sqlitebench` | 纯 Go SQLite WAL：每分钟 473 行事务写入、磁盘增长、12h 汇总/单币图表查询 | `go run ./cmd/sqlitebench -days 7` | ~2 min |

`results/*.json` 是 2026-09-26（周六，UTC 16–18 点）运行输出，已提交作为证据快照；`results/cache/`（原始响应缓存，~60 MB）不入库。行情状态随时间变化，复跑数值会不同——关注结论方向是否仍成立。

测试机器：Apple Silicon macOS、Go 1.24.4、家庭宽带（OKX 路由 `x-routed-to: TKY`）。
