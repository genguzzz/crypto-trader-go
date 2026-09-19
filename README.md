# crypto-trader-go

全新的个人 OKX USDT 永续交易研究工具项目（Go 优先）。旧仓库 `~/Code/cryptoBot` **仅供历史参考**；本仓库不包含其代码、配置或凭据。

## 进度

1. **调研：端到端技术蓝图已完成并归档。** [完整需求与技术蓝图](docs/blueprint/README.md)（逐功能、数据契约、评分、开源选型）优先于[初版调研](docs/go-okx-personal-trading-research-2026-09-19.md)；[完整蓝图 OpenSpec 归档](openspec/changes/archive/2026-09-19-end-to-end-trading-tool-blueprint/) 和[初版归档](openspec/changes/archive/2026-09-19-go-okx-personal-trading-research/)。
2. **交互原型：待启动。** 桌面和手机端候选列表、合约详情及仓位分析；使用合成样本。
3. **具体实现：待启动。** Go 行情、候选池、独立多空评分、API/UI；私有仓位只读接入需另行评审。

项目协作约定见 [AGENTS.md](AGENTS.md)，OpenSpec 上下文见 [openspec/config.yaml](openspec/config.yaml)。当前没有运行中的交易系统，也没有已验证的自动交易策略。
