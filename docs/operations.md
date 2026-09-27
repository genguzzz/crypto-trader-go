# 运行与部署（I0–I3 实现，2026-09-26）

## 本机运行

```bash
make build            # 前端 (web/, Vue3+TS+Lightweight Charts) → web/dist 内嵌进 Go 二进制
CTG_AUTH='user:pass' ./bin/ctg -addr 0.0.0.0:8686 -db data/ctg.db   # 不设 CTG_AUTH 则无登录，只应绑 127.0.0.1
make test             # go vet + go test -race + vue-tsc
```

首次启动（空库）：约 30 s 出 5m 临时名单，约 4 min 完成全池 1m/1H 回填进入 `live`，随后后台做 120h 历史回放（拉 rubik 1H OI，约 3 min）。重启：从 SQLite 恢复，约 1 min 追平。

| 参数 / 环境变量 | 默认 | 说明 |
|---|---|---|
| `-threshold` | 5,000,000 | 首次运行的阈值默认值；之后以设置页保存的为准 |
| `-top` | 200 | 活跃池上限 |
| `-categories` | 空=全部 | `1` 加密 / `3` 股票ETF / `4` 商品；用户决定默认全部进池 |
| `-replay-hours` | 336 | 规则历史回放深度（1H 数据，14 天） |
| `-web` | true | false 时只提供 API（前后端分开部署/调试） |
| `-retain-1m` | 720h | 1m K 线、OI 分钟样本保留期（1H/资金费/评分快照长期保留） |
| `CTG_AUTH` | — | `user:pass`，所有路由 HTTP Basic |
| `CTG_PROXY` | — | 出站 HTTP 代理（可带凭据，只从环境变量读，不记录日志） |
| `CTG_DOH` / `-doh` | off | 通过 1.1.1.1 DoH 解析 OKX 域名并按 IP CONNECT |
| `GOMEMLIMIT` | — | 建议 160MiB（实测常驻约 165 MB） |

数据：`data/ctg.db`（SQLite WAL）。全池 1m 约 60 MB/天。

## 家里 NAS / N1（用户选定的 7×24 运行位置）

`make arm64` → `bin/ctg-linux-arm64`（纯 Go，无 CGO）。已在 N1（iStoreOS 22.03，aarch64）实测可启动。

**当前阻塞（2026-09-26 实测）：NAS 无法访问 OKX REST。**
- 直连超时（本地网络对 okx.com 不通）。
- 经 OpenClash（需认证的 7890 端口）：clash 的 DNS 把 `www.okx.com` 等解析成 `169.254.0.2`，命中 `GeoIP/lan → DIRECT`，失败；按真实 IP 直连 CONNECT 也会被 SNI 嗅探还原成域名再走 DIRECT。
- 同一请求在跳板机 dmit 上直接 200，说明只要让 okx.com 走代理节点即可。

需要用户同意后在 OpenClash 加一条规则（放在 GeoIP/lan 之前），例如自定义规则：
`DOMAIN-SUFFIX,okx.com,🐟 漏网之鱼`（或任一能访问 OKX 的非美国策略组），然后重启 OpenClash（全家网络会短暂中断）。

完成后部署：
```bash
scp bin/ctg-linux-arm64 root@192.168.1.2:/mnt/usb1_2-1/Configs/ctg/ctg
scp deploy/nas/ctg.init root@192.168.1.2:/etc/init.d/ctg
ssh root@192.168.1.2 'chmod +x /etc/init.d/ctg && /etc/init.d/ctg enable && /etc/init.d/ctg start'
# 可选登录：在 NAS 上写 /mnt/usb1_2-1/Configs/ctg/auth（内容 user:pass，chmod 600）
```
内存：N1 可用约 340 MB 且无 swap，服务设 `GOMEMLIMIT=160MiB`、`GOGC=50`；数据放 HDD（`/mnt/usb1_2-1`）避免 eMMC 磨损。

## macOS 本机对局域网开放

macOS 应用防火墙开启，未签名的 `bin/ctg` 入站连接会被拦（局域网访问超时）。需要在本机点“允许传入连接”，或：
`sudo /usr/libexec/ApplicationFirewall/socketfilterfw --add $PWD/bin/ctg --unblockapp $PWD/bin/ctg`
