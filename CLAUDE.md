# crypto-trader-go

个人 OKX USDT 永续研究台：Go 后端（录制/监控池/事实面板/规则/战绩，HTTP+SSE）+ Vue 3 前端，SQLite 单库。只用公共接口，**不读账户、不下单**。

- 先读：[README](README.md) → [架构](docs/architecture.md) → [里程碑与待办](docs/roadmap.md)。协作约束见 [AGENTS.md](AGENTS.md)。
- 开发流程、命令、验证方法：用项目 skill `ctg-dev`（`.claude/skills/ctg-dev/SKILL.md`）。
- 用中文和用户沟通（用户中文更顺）。
- 改代码后必须：`make test`、重建 `bin/ctg` 并重启本地服务、用浏览器（playwright-core + 本机 Chrome）截图确认页面。
- 线上：https://ctg.gugenzzz.top（zouter Docker）。用户要求上线时运行 `deploy/deploy.sh`（替换二进制，不重建镜像，失败自动回滚），数据在服务器 SQLite，不要覆盖或重新下载。
- 前端改动遵守 [界面设计规范](docs/design-guidelines.md)：三级信息层级、语义色、说明文字收进 InfoTip/Modal，不在页面上堆字。
- 新需求先记入 `docs/roadmap.md`；改口径/加数据源/涉及账户权限的先与用户确认。低优先级的“以后要做”只记待办，不提前实现。
- 不读取、打印或提交任何凭据（OKX key、代理密码、`data/.auth` 等）。
