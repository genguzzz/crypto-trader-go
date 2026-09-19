# crypto-trader-go

项目级指引请先阅读同目录的 [AGENTS.md](AGENTS.md)；其适用于 Claude/Codex 和本仓库全部模块。**阶段 1 完整研究蓝图已归档**；等待用户启动阶段 2 原型，不要把阶段 2 或阶段 3 Go 实现当作已授权施工。后续阶段分别通过新的 OpenSpec change 管理。旧 `~/Code/cryptoBot` 的 Python/Top100/24h 技术报告仅供参考，新仓库不继承其代码或凭据，新目标是 Go 优先、Top200、滚动 12h USDT 成交额独立多空评分。最新决策入口为 `docs/blueprint/README.md`；初版调研见 `docs/go-okx-personal-trading-research-2026-09-19.md`，工作流上下文见 `openspec/config.yaml`。不读取或打印私有凭据。
