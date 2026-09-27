# 运行与部署

## 线上

| 项 | 值 |
|---|---|
| 地址 | https://ctg.gugenzzz.top（暂无登录，公开可访问；登录见待办） |
| 机器 | zouter（本机 SSH 别名 `zouter`，地址只在 `~/.ssh/config` 与 GitHub Secrets 中；Debian 12，1C/1G，x86_64，可直连 OKX） |
| 进程 | Docker 容器 `ctg`（compose 项目 `/opt/ctg`），`network_mode: host`，监听 `127.0.0.1:8686` |
| 入口 | nginx stream 按 SNI 默认把 `*.gugenzzz.top` 送到 `127.0.0.1:8444` → `/etc/nginx/conf.d/gateway-ctg.conf` 反代到 8686（SSE 关缓冲）；80 跳 443；证书为已有 `*.gugenzzz.top` 通配符 |
| DNS | Cloudflare `ctg.gugenzzz.top` A/AAAA → zouter，灰云直连 |
| 数据 | `/opt/ctg/data/ctg.db`（SQLite WAL，含行情、设置、规则、自选、战绩快照）+ `data/icons/` |

服务器目录：

```
/opt/ctg/
├── docker-compose.yml   # 来自仓库 deploy/docker-compose.yml
├── ctg.env              # 可选额外环境变量（如 CTG_PROXY），600
├── bin/ctg              # 当前二进制（ctg.prev = 上一版，用于回滚）
└── data/                # 属主 65532（容器 nonroot 用户）
```

## 自动部署（GitHub Actions）

push 到 `main`（纯文档改动除外）→ `.github/workflows/deploy.yml`：测试（vet / race / typecheck）→ 构建 → `deploy/deploy.sh` 热替换 → 公网健康检查。失败时脚本自动回滚到上一版。也可手动触发：`gh workflow run deploy`，查看：`gh run list -w deploy`、`gh run watch`。

- 服务器地址、专用部署私钥、主机公钥只存在 GitHub Secrets（`DEPLOY_HOST` / `DEPLOY_SSH_KEY` / `DEPLOY_KNOWN_HOSTS`），日志里自动打码；仓库为私有。
- 一次性配置（在能 SSH 到服务器的机器上）：`deploy/setup-ci.sh`；吊销：`deploy/setup-ci.sh --revoke`。
- 适合云端会话：改代码 → `make test` → push → `gh run watch` → 访问线上验证，全程不需要 SSH。

## 手动部署（本机）

```bash
deploy/deploy.sh               # 构建前端 + linux/amd64 二进制 → 上传 → 替换 → 重启容器 → 健康检查（失败自动回滚）
deploy/deploy.sh --status      # 容器状态 + 最近日志
deploy/deploy.sh --rollback    # 回到上一版二进制
deploy/deploy.sh --skip-build  # 上传已构建的 bin/ctg-linux-amd64
```

- **不重建镜像**：容器用官方 `gcr.io/distroless/static-debian12:nonroot`（2 MB，只有 CA 证书/时区/非 root 用户），二进制通过 `./bin:/app:ro` 挂载；升级只替换 `bin/ctg` 并重启，全程约 30 秒。
- **数据复用**：重启后从 SQLite 恢复，只补停机期间缺口（约 40 秒追平）；设置、规则、自选都在数据库里，不受升级影响。
- 改了 `deploy/docker-compose.yml` 时运行一次 `deploy/deploy.sh --init`（幂等：建目录、同步 compose 文件、修正数据属主、`docker compose up -d`）。
- 其他主机：`CTG_HOST=root@host CTG_ARCH=arm64 deploy/deploy.sh --init && deploy/deploy.sh`，另需按该机的 nginx 方式反代 `127.0.0.1:8686`。

## 本机开发

```bash
make build                     # 前端产物内嵌进 bin/ctg
./bin/ctg                      # http://127.0.0.1:8686，数据 data/ctg.db
./bin/ctg -web=false & (cd web && npm run dev)   # 前后端分开调试，Vite 代理 /api
make test                      # go vet + go test -race + vue-tsc
```

首次（空库）启动：约 30 秒出 5m 临时名单，约 4 分钟全池回填进入 `live`；随后后台 14 天回放与 7 天 1m 历史回补（进度见“系统”页）。迁移数据到新机器：`sqlite3 data/ctg.db ".backup /tmp/ctg.db"` 在线备份后复制到目标 `data/`。

## 参数与环境变量

| 参数 / 变量 | 默认 | 说明 |
|---|---|---|
| `-addr` / `CTG_ADDR` | `127.0.0.1:8686` | 监听地址 |
| `-db` / `CTG_DB` | `data/ctg.db` | SQLite 路径 |
| `-threshold` / `-top` / `-categories` | 5,000,000 / 200 / 全部 | 仅首次运行默认值；之后以设置页保存的为准 |
| `-replay-hours` | 336 | 规则历史回放深度（14 天） |
| `-retain-1m` | 720h | 1m K 线与 OI 分钟样本保留期；1H、资金费、评分快照长期保留 |
| `-web` | true | false 时只提供 API |
| `CTG_PROXY` / `-doh` | — / off | 无法直连 OKX 的网络：出站 HTTP 代理（可带凭据）、DoH 解析 |
| `GOMEMLIMIT` | compose 中 180MiB | 常驻约 175 MB；容器 `mem_limit: 400m` |

全池 1m 约 60 MB/天；当前库约 450 MB。

## 排查

- `deploy/deploy.sh --status` 看日志；关键行：`restored … bars`、`bootstrap complete`、`ws … disconnected`。
- 页面“系统”：阶段、覆盖率（缺口数）、WS 重连、REST 限频、回放与历史回补进度。
- nginx：`nginx -t && systemctl reload nginx`；站点配置 `/etc/nginx/conf.d/gateway-ctg.conf`，不需要改 `stream.conf`。
