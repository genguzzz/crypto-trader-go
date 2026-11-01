# Agent rules — crypto-trader-go

Rules for every AI agent (Claude Code, Codex, …) working in this repository. Read [README](README.md) → [architecture](docs/architecture.md) → [decisions](docs/decisions.md) → [roadmap](docs/roadmap.md) first.

## Product constraints (don't change without the owner)

- Public OKX market data only. **Never place real orders** except through an approved trade proposal: on 2026-11-01 the owner approved real orders only as proposals he approves himself (`internal/okxorder` + `internal/realtrade`; open with TP/SL, add, reduce, close and TP/SL adjustment; market or limit; isolated or cross), dry run until he switches `CTG_TRADE_MODE=live`; never test with real orders (httptest fakes only), never widen the live client's allow-list beyond what an approved kind needs, never add a way around the approval. Never read the OKX account without explicit approval (approved 2026-11-01: the owner's open positions and TP/SL, and read-only queries of his account for the agent (`okx_api`: deposits, withdrawals, bills, history); `internal/okxacct` stays GET-only). Simulated (paper) trading is approved (2026-10-02, [trading design](docs/trading-design.md)); `ModeReal` stays rejected.
- Pool definition: 12h turnover = `volCcyQuote` over 720 consecutive confirmed 1m candles, strictly above the threshold (default 5M USDT), top N. A missing minute is a gap, not zero. `volCcy24h` is not USDT turnover.
- Scores come only from user rules over pool percentiles, are always labeled unvalidated, and only the track record (out-of-sample Rank IC, net-of-cost returns) can support them. Long/short are independent; missing input means unscored, never 0.
- Live and replay share `features.Compute`; hourly snapshots are immutable; preview values never enter the track record.
- Changing a definition, adding a data source or touching account permissions needs the owner's confirmation.

## Working rules

- **English** in all docs, comments, commits, issues and PRs. Talk to the owner in Chinese. Exceptions approved by the owner: the comments in built-in strategies (`strategies/library/`), editor templates, the in-app help articles (`web/src/docs/`) and the owner's research reports (`docs/research/*.html` with their `research/report/history.json`; 2026-10-11) are Chinese, because they are teaching material for the owner.
- Work from an issue ([requirements](docs/contributing.md#requirements), [bugs](docs/contributing.md#bugs)); claim it before starting ([claiming work](docs/contributing.md#claiming-work)).
- Parallel work uses one worktree per issue and a self-reviewed pull request: [worktree workflow](docs/worktree-workflow.md). Direct commits to `main` only when the owner asks.
- Commits and PR titles follow [Conventional Commits](docs/contributing.md#commits). `git pull --rebase` before pushing; never force-push `main`.
- After any code change: `make test`, `scripts/dev.sh restart`, `scripts/dev.sh shots` (desktop + phone). Fix until clean.
- Frontend follows the [design guidelines](docs/design-guidelines.md): three-level hierarchy, semantic color only, explanations in InfoTip/Modal, component library only, no horizontal overflow on phones.
- Record times as exchange time and receive time; state units (USDT / coins / contracts); unknown stays unknown.
- Never read, print or commit credentials (OKX keys, proxy passwords, the server's `ctg.env`, alert channel secrets). Never write server hostnames or IPs into the repository; they live in GitHub Secrets and `~/.ssh/config`.
- The production database is the single source of truth: never overwrite it or re-download it.
- Low-priority "later" ideas become issues, not code.
