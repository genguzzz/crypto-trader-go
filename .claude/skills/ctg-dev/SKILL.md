---
name: ctg-dev
description: crypto-trader-go（OKX 研究台）开发与验证流程：构建、测试、重启本地服务、浏览器截图核对、常见坑。改本仓库 Go 后端或 Vue 前端、加字段/规则/页面、排查数据问题时使用。
---

# ctg-dev

## 目录速记
- 后端入口 `cmd/server`；引擎 `internal/market`（engine 启动与录制、universe 每分钟池、preview 10 秒盘中、rules 回放与战绩、history 7 天 1m 回补、settings 运行时设置、queries 图表/详情/状态）。
- 公式：`internal/features`（加字段在 `Fields` 登记 + `Compute` 计算；仅实时字段在 `market/universe.go: liveValues`）。
- 规则：`internal/rules`（预置规则 `Presets()`）；战绩：`internal/track`。
- 接口：`internal/api/api.go`；前端：`web/src/{views,components}`，类型在 `web/src/api.ts`。

## 前端规范速查
- 读 `docs/design-guidelines.md`。说明/口径 → `<InfoTip>`（全局组件，放 h3 旁），公式 → `<ScoringExplainer />`，明细 → `<Modal>`；页面只放动态状态。
- 控件用组件库：`<Segmented>`、`<USelect>`（全局）、`ui/ChipGroup`、`ui/FilterChip`、`ui/SearchInput`、Reka `Popover`；布局提示进顶部布局菜单（`useLayoutEntry`）。
- 截图脚本同时检查手机 `document.documentElement.scrollWidth === 390`。
- 颜色/字号只用 `style.css` token；指标“标签在上、数值在下”；PC 专属交互用 `composables/device.ts` 的 `useDesktop()`。

## 常用命令
```bash
make test                                   # go vet + go test -race + vue-tsc
cd web && npm run build && cd ..            # 前端产物 → web/dist（被 go:embed）
go build -o bin/ctg ./cmd/server
pkill -f 'bin/ctg -addr'; GOMEMLIMIT=160MiB nohup ./bin/ctg -addr 127.0.0.1:8686 -db data/ctg.db > data/server.log 2>&1 &
until grep -q 'bootstrap complete' data/server.log; do sleep 2; done   # 重启约 1 分钟
curl -s localhost:8686/api/v1/health                                 # phase=live、stale=0
```
前后端分开：`./bin/ctg -web=false` + `cd web && npm run dev`（Vite 代理 /api）。

## 验证页面（必须做）
用 playwright-core 驱动本机 Chrome（`/Applications/Google Chrome.app/Contents/MacOS/Google Chrome`），打开页面等 4–5 秒后截图并检查 `pageerror`；桌面 1600×900 与手机 400×860 各看一次。不要用 Chrome `--headless --virtual-time-budget`（SSE 长连接会卡住）。

## 坑
- 本机 shell 有 Surge 代理环境变量，Go 默认 Transport 会走它；NAS 上需 `CTG_PROXY`（OpenClash 需认证且 okx.com 被错误直连，见 docs/operations.md）。
- go.mod 要求 Go 1.25（modernc sqlite 依赖）。
- 1m 内存是 1500 分钟环形缓冲，更早的只在 SQLite；图表查询走 DB + 内存合并。
- OKX `/market/candles` 1m 只给最近 1440 根，更早用 `/market/history-candles`（每页 300，单独限频）。
- rubik OI 历史：5m 约 5 天、1H 约 60 天，时间戳按“该小时开始”保守对齐。
- 改设置会触发监控池重算与 14 天回放重跑（后台）；回放 OI 已拉过的币会跳过。
