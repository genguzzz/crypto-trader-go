## Why

旧仓库 `cryptoBot` 既有 2026-09-12 调研针对 Python + Top100/24h 的实时监控提出建议；现在目标已变为 **Go 优先、Top200、滚动 12h USDT 成交额、多空独立评分、持仓离场分析** 的个人研究工具。旧结论不能直接复用，否则会混淆成交额窗口、信号与执行权限、实时性和评分有效性。先完成可追溯的开源/官方 API 调研，再分别推进交互原型和实现。

## What Changes

- 调研 GitHub Go 组件及 OKX 官方数据、WebSocket/REST 限频、12h 成交额口径、私有仓位权限；明确自建/复用边界。
- 设计可解释的双向候选分、仓位离场分及数据质量/风险标签的研究假设和回测验证协议，不将经验规则冒充预测结论。
- 给出桌面/移动端信息架构和阶段 2 的原型验收目标，不在阶段 1 编写 UI 或交易系统。
- 将项目路线、阶段边界、凭据安全及 OpenSpec 工作流写入项目根目录 AGENTS.md/CLAUDE.md，并更新 OpenSpec 项目上下文。
- 记录与历史 Python/Top100/24h 方案的差异；旧实现保持原样，不作为新仓库代码来源。

## Capabilities

### New Capabilities

- `go-okx-trading-product-research`: 维护新产品调研、数据定义、开源方案取舍、阶段规划及验证标准。

### Modified Capabilities

无；旧 `contract-platform-architecture-research` 与 `okx-market-data-validation` 是此前阶段的历史验收事实，本变更不改写其已经归档的需求。

## Impact

仅新增研究文档、项目级 agent 指引和 OpenSpec 文档；修改 `openspec/config.yaml` 的项目方向描述。本阶段不读取私有密钥、不调用私有 API、不做真实交易，不迁移/重构旧 Python 服务。阶段 2/3 将各自创建新 OpenSpec change。
