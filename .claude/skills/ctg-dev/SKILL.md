---
name: ctg-dev
description: Build, run and verify crypto-trader-go (OKX research desk) — Go backend + Vue frontend. Use for any code change in this repo (backend fields/rules/APIs, frontend pages/components, data issues), and to restart the local server or take verification screenshots.
---

# ctg-dev

## Map

| Where | What |
|---|---|
| `cmd/server` | entry point, flags |
| `internal/market` | engine: `engine.go` startup/recording/WS, `universe.go` per-minute pool and `liveValues`, `preview.go` 10 s preview, `rules.go` replay and track record, `history.go` 1m backfill, `catalog.go` search catalog and scanner rows (`buildRow`), `retention.go`, `liquidations.go`, `queries.go` chart/detail/status |
| `internal/features` | fact-panel formulas: register in `Fields`, compute in `Compute`; live-only fields in `market/universe.go: liveValues` |
| `internal/rules` / `internal/track` / `internal/alerts` | rules and presets (`Presets()`), track-record stats, monitoring |
| `internal/api/api.go` | HTTP routes |
| `web/src` | `views/` pages, `components/` (ui library in `components/ui`), types in `api.ts`, formatting in `fmt.ts`, tokens in `style.css` |
| `web/ios`, `web/native`, `web/src/native.ts` | iOS app (Capacitor) and its over-the-air updates — see `docs/ios.md`; API paths that bypass `fetch` need `apiUrl()` |

## Commands

```bash
make test                     # go vet + go test -race + vue-tsc — must pass
scripts/dev.sh restart        # build web + bin/ctg, restart this checkout's server on its port, wait until live
scripts/dev.sh status         # port, pid, health
scripts/dev.sh shots [path]   # desktop 1600×900 + phone 400×860 screenshots → data/shots/; fails on page errors or phone overflow
scripts/dev.sh stop
```

- Port: `data/port` in a worktree, else 8686. Frontend-only iteration: `./bin/ctg -web=false` + `cd web && npm run dev` (Vite proxies `/api`).
- Look at the screenshots (Read the PNGs) — a passing script is not visual verification. For a specific state (a page, a scrolled table, a setting), write a small playwright-core script next to `web/scripts/shot.mjs`.
- A restart catches up in ~1 min; a fresh database needs ~4 min to go live.

## Workflow

1. Work from an issue; parallel work goes through the `ctg-worktree` skill.
2. Change code → `make test` → `scripts/dev.sh restart` → `scripts/dev.sh shots` → check the images → iterate.
3. Update docs with the change: `docs/architecture.md` (data, algorithms, APIs), `docs/operations.md` (flags, deploy), `docs/roadmap.md` (Shipped row).
4. Commit in Conventional Commits format (`docs/contributing.md#commits`).

## Frontend rules (details: docs/design-guidelines.md)

- Explanations go into `<InfoTip>` next to the `h3`; formulas → `<ScoringExplainer />`; large detail → `<Modal>`. Pages show only live state.
- Controls come from the library: `<Segmented>`, `<USelect>` (global), `ui/ChipGroup`, `ui/FilterChip`, `ui/SearchInput`, `ui/Switch`, Reka `Popover`. Layout hints go into the top Layout menu (`useLayoutEntry`).
- Colors and sizes only via `style.css` tokens; metric = label on top, value below; funding colored with `fundingClass()`.
- PC-only interactions behind `useDesktop()`; phone width must equal the viewport.

## Gotchas

- Local shells carry Surge proxy variables that Go's default transport uses; the server connects to OKX directly. Networks that can't reach OKX: `CTG_PROXY` / `-doh`.
- Go 1.25 is required (modernc sqlite). On the dev box `/usr/bin/go` is older: `export PATH=/usr/local/go/bin:$PATH`; `-race` fails there (ThreadSanitizer VMA), so run `go vet ./... && go test ./...` instead of `make test`.
- `GOMEMLIMIT` must stay well above the ~180 MB resident heap (scripts use 384MiB); a limit near it makes the GC spin at 100%+ CPU.
- 1m candles in memory are a 1500-minute ring; older ones only in SQLite — chart queries merge DB + memory.
- OKX `/market/candles` gives only the latest 1440 1m bars; older via `/market/history-candles` (300 per page, separate rate limit).
- rubik OI history: 5m ≈ 5 days, 1H ≈ 60 days; timestamps aligned conservatively to the bucket start.
- Saving settings triggers a pool recompute and a background 14-day replay; instruments whose OI was already fetched are skipped.
- `web/dist` is embedded via `go:embed`: rebuild the web before `go build` (the script does).
