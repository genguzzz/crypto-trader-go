---
name: ctg-platform
description: How the OKX Helper (ctg) app itself works — pages, features, fields, units, scores, charts, data freshness — and how to link the owner to the right page. Use when the owner asks how to use the app or what something on screen means, where to find something, or how a number is computed, e.g. "怎么用", "这个页面", "这个数是什么意思", "在哪看", "规则分", "多头分/空头分", "战绩/IC", "因子", "事实面板", "监控池", "扫描", "清算图怎么算", "足迹/POC", "告警", "监控", "模拟交易", "回测", "策略", "AI 审核", "数据多久更新". Also read it before recommending coins, rules or pages, for the in-app link format. Not for market analysis of a coin (use perp-analysis).
---

# OKX Helper（ctg）平台说明

OKX Helper 是主人自用的 **OKX USDT 本位永续合约研究台**：实时找出流动性够的合约（监控池），客观展示每个币的状态（事实面板），用主人自定义的规则打分，并自动用之后的真实行情检验每条规则（战绩）。只用 OKX **公共**行情：不读账户、不下真实订单；交易页全部是模拟盘。分数是规则排序，不是胜率。界面时间默认 UTC+8。

## 页面

| 页面 | 应用内路径 | 一句话 |
|---|---|---|
| 扫描 | `/` | 监控池列表：多/空分、资金费、涨跌、OI 变化、放量、市值、12h 成交额；搜索、筛选、自选；PC 上右侧是合约工作台 |
| 合约详情 | `/instrument/<instId>` | 价格与关键数、评分卡、K 线图（Delta/足迹/现货/OI/强平/清算图）、盘口、相对 BTC/ETH、OI 与资金费历史、强平 |
| 规则与战绩 | `/rules`、`/rules/<ruleId>` | 编辑多空权重与点差门槛（每次保存 = 新版本）；Rank IC、t 值、前 20% 超额/净收益 |
| 监控告警 | `/alerts` | 价格异动 / 规则分数 / 资金费 三类监控规则，Telegram / Webhook 推送，最近触发 |
| 模拟交易 | `/trading` | 模拟账户（实时模拟、历史回测）、风控、策略实例、成交与报告 |
| 策略库 | `/trading/strategies` | 内置与自建 Python 策略，编辑、发布、回测 |
| 待确认 / AI 决策 | `/trading/prompts`、`/trading/ai` | 人工确认的信号；AI 审核的每次决策、参数建议、经验文档 |
| 系统状态 | `/status` | 阶段、数据新鲜度、覆盖与缺口、OKX 连接、存储与保留期、后台任务进度 |
| 设置 | `/settings` | 监控池阈值/上限/类别、默认规则、存储上限、时区、持仓量条、开发者、交互通道、AI 授权 |
| 帮助 | `/help/strategy/<slug>` | 策略编写帮助文章（见 paper-trading-and-strategies.md） |

手机底栏：扫描、规则、交易、设置 + “更多”（监控、帮助、系统等）。PC 上打开 `/instrument/<instId>` 会转到扫描页的分栏工作台（`/?sel=<instId>`），链接照样可用。

## 应用内链接（必须遵守）

聊天会把**目标是应用内路径**的 Markdown 链接渲染成可点按钮，点一下就在应用里跳转。提到主人能打开的东西时一律写成这种链接：

- 合约（一定用完整 instId）：`[SOL](/instrument/SOL-USDT-SWAP)`
- 规则及其战绩：`[残差反转](/rules/resid-reversal)`，即 `[规则名](/rules/<ruleId>)`（ruleId 用 MCP `list_rules` 查）
- 页面：`[扫描](/)`、`[规则与战绩](/rules)`、`[监控告警](/alerts)`、`[模拟交易](/trading)`、`[策略](/trading/strategies)`、`[系统状态](/status)`、`[设置](/settings)`、`[帮助](/help/strategy/<slug>)`

只用上面这些应用路径；外部来源仍写普通 https 链接。例：推荐 3 个币时，每个币名都写成 `/instrument/...` 链接；说“去看战绩”时直接给规则链接。

## 回答原则

- 先查实时定义再下结论：字段用 MCP `list_fields`，规则用 `list_rules`，战绩用 `get_rule_track_record`，数据是否新鲜用 `get_system_status`。下面的参考文件讲“怎么算、怎么读”，具体数值以工具返回为准。
- 缺失 = 未知（显示“—”/未评分），不要当 0 解读。引用规则分时说明它有没有显著的样本外战绩（Rank IC、t 值）支撑，不要写免责套话。
- 不确定的功能不要编；说明“应用里没有这个功能”比猜测好。

## 参考文件（按需读）

| 文件 | 什么时候读 |
|---|---|
| [references/pool-and-scanner.md](references/pool-and-scanner.md) | 监控池怎么定义、为什么某币不在池里/被移出、扫描页各列/筛选/排序、自选、顶栏状态 |
| [references/facts-and-scores.md](references/facts-and-scores.md) | 事实面板字段与单位（对数收益、资金费/小时等）、分位、规则分公式、门槛、未评分、战绩（Rank IC、t 值、净收益）怎么读、内置对照规则 |
| [references/instrument-page.md](references/instrument-page.md) | 详情页各区块；K 线图图层、周期、现货/OI Δ/OI/强平、足迹、POC/价值区/失衡、清算图模型及与 CoinGlass 的差别、刷新频率；盘口与深度图 |
| [references/alerts.md](references/alerts.md) | 监控规则类型与参数、触发与冷却、推送渠道、触发历史 |
| [references/paper-trading-and-strategies.md](references/paper-trading-and-strategies.md) | 模拟账户、风控与仓位公式、策略实例与执行方式（自动/AI 审核/只记录）、人工确认、回测报告指标、策略库、帮助文章 slug |
| [references/data-sources-and-freshness.md](references/data-sources-and-freshness.md) | 哪些数据本机录制、哪些临时拉取、保留期与分层、启动阶段、时区、系统状态页健康检查 |
