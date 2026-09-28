# crypto-trader-go · OKX Helper

A personal **research desk for OKX USDT-margined perpetual swaps**: it finds contracts with enough liquidity in real time, shows each coin's state objectively (the fact panel), scores them with rules you define, and automatically checks each rule against what the market did next (the track record).

> Uses OKX **public** market data only: no API key, no account access, **no orders**. A score is a rule-based ranking, not a win rate or trading advice.

Live: **https://ctg.gugenzzz.top** (greencloud, Docker; no login yet, see the [roadmap](docs/roadmap.md)).

![Scanner](docs/images/scanner.png)

## Features

| Page | What it does |
|---|---|
| Scanner | Monitoring pool (default: 12h turnover > 5M USDT, up to 200 contracts across crypto / stock / commodity perps); sort by long/short score or any field, filter, watchlist; open-interest bar per row; split view on PC, separate detail page on phones |
| Detail | Ticker cards that open into detail: live order book, USDT depth chart (hover shows the capital needed to move price x%), turnover, open interest, funding, basis; score breakdown; K-line (1m–4H, optional spot overlay, delta and footprint); relative performance vs BTC/ETH; OI and funding history |
| Rules & track record | Edit long/short weights and the spread gate (each save is a new version); per rule 1h/4h/24h Rank IC, t-stat, top-quintile excess and net-of-cost return; 14-day replay plus an immutable live record |
| Monitoring | Alert rules (price move, score, funding) evaluated every confirmed minute, delivered via Telegram / webhook |
| Settings | Threshold, cap N, categories, default rule, storage cap, display options; applied on save |
| System | Phase, coverage, WS connections, REST usage, storage, replay and backfill progress |

<p>
  <img src="docs/images/rules.png" width="68%" alt="Rules and track record" />
  <img src="docs/images/mobile.png" width="26%" alt="Mobile" />
</p>

## How it works

1. **Record**: on start, load the instrument list, subscribe to 1m candles for every candidate over WebSocket, fill gaps over REST, write to SQLite (WAL).
2. **Monitoring pool**: every minute, recompute USDT turnover over the last 720 closed 1m candles; take the top N above the threshold.
3. **Fact panel**: returns, strength vs BTC, OI change, volume surprise, funding, basis, and more; live and replay use the same formulas.
4. **Scoring**: each field is ranked within the pool and mapped to ±1, then weighted by the rule: `50 + 50·Σwx/Σ|w|`. A 10-second preview uses the open minute; an immutable snapshot is taken every hour.
5. **Track record**: Rank IC, t-stat and quantile returns from the returns realized after each snapshot tell whether a rule works.

See [architecture](docs/architecture.md) for details.

## Stack

| Layer | Choice |
|---|---|
| Backend | Go 1.25 single binary; stdlib `net/http` (REST + SSE); web UI embedded via `go:embed` |
| Storage | SQLite WAL (`modernc.org/sqlite`, pure Go, no CGO) |
| Data | OKX v5 public REST / WebSocket (own rate limiting, self-healing reconnects, optional proxy/DoH) |
| Frontend | Vue 3 + TypeScript + Vite; Reka UI; lightweight-charts; lucide icons |
| Deploy | distroless runtime image + bind-mounted binary (upgrades don't rebuild the image); nginx reverse proxy |

## Quick start

Requires Go 1.25+ and Node 22+.

```bash
git clone https://github.com/genguzzz/crypto-trader-go.git && cd crypto-trader-go
(cd web && npm ci)
make build                     # build the web UI and embed it into bin/ctg
./bin/ctg                      # http://127.0.0.1:8686, data in data/ctg.db
make test                      # go vet + go test -race + vue-tsc
```

Backend and frontend separately:

```bash
./bin/ctg -web=false           # API only (:8686)
cd web && npm run dev          # Vite dev server, proxies /api to :8686
```

First start: a provisional list after ~30 s, the full pool is backfilled and live after ~4 min; then a 14-day replay and a 7-day 1m backfill run in the background. A restart catches up in ~1 min.

## Deploy

Pushing to `main` runs tests and deploys through GitHub Actions (`.github/workflows/deploy.yml`; run `deploy/setup-ci.sh` once to configure secrets). Manual deploy from a workstation:

```bash
deploy/deploy.sh               # build → upload binary → swap → restart container → health check (auto-rollback), ~30 s
deploy/deploy.sh --rollback    # back to the previous binary
deploy/deploy.sh --status      # container state and logs
```

Data and settings live in the server's SQLite and survive upgrades. See [operations](docs/operations.md).

## Layout

```
cmd/server/        entry point and flags
internal/okx/      OKX REST/WS adapter (rate limits, reconnects, proxy)
internal/market/   engine: recording, pool, preview, rule replay, order book, queries
internal/features/ fact-panel formulas
internal/rules/    rule definitions and scoring
internal/track/    track-record statistics
internal/alerts/   monitoring rules and delivery channels
internal/store/    SQLite storage
internal/api/      HTTP routes, SSE, icon proxy, gzip
web/               Vue frontend (build output embedded into the binary)
deploy/            docker-compose and deploy scripts
docs/              architecture, decisions, operations, conventions, design guidelines, roadmap
research/poc/      reproducible probes from the research phase
```

## Documentation

| Doc | Contents |
|---|---|
| [Architecture](docs/architecture.md) | Modules, data flow, key algorithms, technology choices |
| [Operations](docs/operations.md) | Production, deploy/rollback, local development, flags, troubleshooting |
| [Contributing](docs/contributing.md) | Requirement, bug, commit and pull request conventions |
| [Worktree workflow](docs/worktree-workflow.md) | Parallel development with git worktrees and self-reviewed pull requests |
| [Design guidelines](docs/design-guidelines.md) | Information hierarchy, design tokens, UI rules |
| [Roadmap](docs/roadmap.md) | Done / in progress / backlog |
| [Product decisions](docs/decisions.md) | Principles, definitions and measured facts behind the design |
| [Trend-signal research](docs/research-trend-signals.md) | Why scores are rules to validate, not preset directions |
| [Research probes](research/poc/README.md) | Public-API measurement programs and results |

## Disclaimer

For personal research only; nothing here is investment advice. Crypto assets and derivatives are extremely risky; use at your own risk.
