# crypto-trader-go 协作指引

适用于 Claude/Codex 与本仓库全部目录。

## 项目现状（2026-09-27）

- 独立新项目，**不是** `/Users/gugen/Code/cryptoBot` 的延续；旧仓库只读参考，不复制代码/配置/凭据。
- 已实现 I0–I3：见 `README.md`、`docs/architecture.md`；进度与待办只维护在 `docs/roadmap.md`。
- 决策依据：`docs/blueprint/revision-2026-09-26.md`（实测修订，优先于旧蓝图各专题）；调研探针 `research/poc/`（独立 module，非生产代码）。

## 产品约束（不可随意改变）

- 监控池口径：连续 720 根已确认 1m K 线 `volCcyQuote` 之和严格大于阈值（默认 500 万 USDT，设置页可改），降序取前 N；缺分钟是数据缺口，不是 0 成交；`volCcy24h` 不能当 USDT 成交额。
- 分数只来自用户规则（事实面板池内分位加权），始终标“未验证”；只有战绩（样本外 Rank IC、扣费净收益）能说明规则有用；多/空独立；缺输入即未评分，不补 0。
- 实时与回放共用 `features.Compute`；整点快照不可改写；预览（10 秒）值不进战绩。
- 只用 OKX 公共接口；不下单；只读账户（I4）需用户另行批准。

## 界面规范（必须遵守，详见 `docs/design-guidelines.md`）

- 信息分“主/次/弱”三级（颜色 `--text-1/2/3` + 字号字重），颜色只表达涨跌/警示/交互语义；数字等宽。
- **说明文字不直接铺在页面上**：口径、公式、数据来源放 `<InfoTip>`（PC 悬浮、触屏点击），大量明细用 `<Modal>`；页面上只留动态状态与一句话必要警示。
- 使用 `web/src/style.css` 的 token，不写零散颜色/字号；PC 专属交互用 `useDesktop()` 判断并给触屏等价方案。
- 交互控件只用组件库（Segmented / USelect / ChipGroup / FilterChip / SearchInput / Reka Popover / StatCard），不写原生 select 或手拼按钮组；PC 只有内容区滚动；手机页面不得横向溢出。

## 工作方式

- 跨模块或改变口径的改动走 OpenSpec（`openspec/changes/<name>` → 完成后归档到 `archive/`），并更新 `docs/roadmap.md`。
- 每次改动：`make test`（go vet、`go test -race`、vue-tsc），真实启动服务并用浏览器截图核对；失败要修到通过。
- 按交易所时间、接收时间记录数据；单位写清楚（USDT/币/张）；不可得就显示未知。
- 不读取、输出或提交密钥、Cookie、代理密码、私有仓位；`data/` 已忽略。
