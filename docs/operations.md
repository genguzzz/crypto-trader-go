# Operations and deployment

## Production

| Item | Value |
|---|---|
| URL | https://ctg.gugenzzz.top (no login yet, publicly reachable; login is on the roadmap) |
| Host | greencloud (SSH alias `greencloud`; the address lives only in `~/.ssh/config` and GitHub Secrets; Debian 13, 2C/4G, x86_64, GreenCloud Tokyo, direct access to OKX; moved from zouter on 2026-09-27) |
| Process | Docker container `ctg` (compose project `/opt/ctg`), `network_mode: host`, listening on `127.0.0.1:8686` |
| Ingress | nginx stream routes `*.gugenzzz.top` by SNI (default) to `127.0.0.1:8444` → `/etc/nginx/conf.d/gateway-ctg.conf` proxies to 8686 (buffering off for SSE); 80 redirects to 443; existing `*.gugenzzz.top` wildcard certificate |
| DNS | Cloudflare `ctg.gugenzzz.top` A/AAAA → greencloud, DNS only (grey cloud) |
| Data | `/opt/ctg/data/ctg.db` (SQLite WAL: market data, settings, rules, watchlist, track-record snapshots) + `data/icons/` |

Server layout:

```
/opt/ctg/
├── docker-compose.yml   # from deploy/docker-compose.yml in the repo
├── ctg.env              # optional extra environment (e.g. CTG_PROXY), mode 600
├── bin/ctg              # current binary (ctg.prev = previous one, for rollback)
└── data/                # owned by 65532 (the container's nonroot user)
```

The server database is the single source of truth. Never overwrite it with a local copy and never re-download it from scratch.

## Automatic deploy (GitHub Actions)

Push to `main` (documentation-only changes excluded) → `.github/workflows/deploy.yml`: tests (vet / race / typecheck) → build → `deploy/deploy.sh` hot swap → public health check. On failure the script rolls back to the previous binary. Trigger manually with `gh workflow run deploy`; follow with `gh run list -w deploy` and `gh run watch`.

- The server address, the dedicated deploy key and the host key exist only in GitHub Secrets (`DEPLOY_HOST` / `DEPLOY_SSH_KEY` / `DEPLOY_KNOWN_HOSTS`) and are masked in logs; the repository is private.
- One-time setup (from a machine that can SSH to the server): `deploy/setup-ci.sh`; revoke with `deploy/setup-ci.sh --revoke`.
- Works from cloud sessions: change code → `make test` → merge to `main` → `gh run watch` → verify online, no SSH needed.
- Merging a pull request into `main` deploys it. Paths ignored by the workflow: `**.md`, `docs/**`, `research/**`, `.claude/**`.

## Manual deploy (workstation)

```bash
deploy/deploy.sh               # build web + linux/amd64 binary → upload → swap → restart container → health check (auto-rollback)
deploy/deploy.sh --status      # container state + recent logs
deploy/deploy.sh --rollback    # back to the previous binary
deploy/deploy.sh --skip-build  # upload an already built bin/ctg-linux-amd64
```

- Run it only from an up-to-date `main` checkout, never from a feature worktree, so production always matches `main`.
- **No image rebuilds**: the container runs the official `gcr.io/distroless/static-debian12:nonroot` (2 MB: CA certificates, time zones, a non-root user) and the binary is mounted via `./bin:/app:ro`; an upgrade swaps `bin/ctg` and restarts, about 30 s end to end.
- **Data is reused**: after a restart the engine restores from SQLite and only fills the downtime gap (~40 s to catch up); settings, rules and the watchlist live in the database and survive upgrades.
- After changing `deploy/docker-compose.yml`, run `deploy/deploy.sh --init` once (idempotent: creates directories, syncs the compose file, fixes data ownership, `docker compose up -d`).
- Another host: `CTG_HOST=root@host CTG_ARCH=arm64 deploy/deploy.sh --init && deploy/deploy.sh`, plus a reverse proxy to `127.0.0.1:8686` in that host's nginx setup.

## Local development

```bash
make build                     # web build embedded into bin/ctg
./bin/ctg                      # http://127.0.0.1:8686, data in data/ctg.db
./bin/ctg -web=false & (cd web && npm run dev)   # separate backend/frontend, Vite proxies /api
make test                      # go vet + go test -race + vue-tsc
```

First start with an empty database: a provisional 5m list after ~30 s, full pool backfilled and `live` after ~4 min; then the 14-day replay and 7-day 1m backfill run in the background (progress on the System page). To move data to another machine, take an online backup with `sqlite3 data/ctg.db ".backup /tmp/ctg.db"` and copy it into the target `data/`.

Parallel development in several worktrees (separate ports and database copies) is described in [worktree workflow](worktree-workflow.md).

## Flags and environment

| Flag / variable | Default | Meaning |
|---|---|---|
| `-addr` / `CTG_ADDR` | `127.0.0.1:8686` | listen address |
| `-db` / `CTG_DB` | `data/ctg.db` | SQLite path |
| `-threshold` / `-top` / `-categories` | 5,000,000 / 200 / all | first-run defaults only; afterwards the Settings page wins |
| `-replay-hours` | 336 | rule replay depth (14 days) |
| `-storage-cap-mb` | 2048 | first-run default storage cap; afterwards Settings → Data storage wins. Per-instrument tiered retention is described in [architecture §3](architecture.md#3-key-algorithms-summary) |
| `-web` | true | false serves the API only |
| `CTG_PROXY` / `-doh` | — / off | networks without direct OKX access: outbound HTTP proxy (credentials allowed), DNS-over-HTTPS |
| `GOMEMLIMIT` | 1536MiB in compose | resident heap is ~180 MB (with the full perpetual catalog and caches); container `mem_limit: 2g`. The limit must stay above the resident heap or the GC spins: 180MiB once caused ~20 GCs per second and 1.4 cores of constant CPU |

The full universe at 1m is ~60 MB/day; the retention job runs every 30 minutes and keeps the database under the storage cap (per-table usage and effective retention on the System page). Freed space is reused inside the file; the file itself doesn't shrink.

## Troubleshooting

- `deploy/deploy.sh --status` for logs; key lines: `restored … bars`, `bootstrap complete`, `ws … disconnected`.
- System page: phase, coverage (gap count), WS reconnects, REST rate limiting, replay and backfill progress.
- nginx: `nginx -t && systemctl reload nginx`; the site config is `/etc/nginx/conf.d/gateway-ctg.conf`; `stream.conf` needs no changes.
- Local shells may carry proxy environment variables (e.g. Surge) that Go's default transport picks up; the server connects to OKX directly.
