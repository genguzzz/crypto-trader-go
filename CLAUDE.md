# crypto-trader-go

Personal OKX USDT-perpetual research desk: Go backend (recording, pool, fact panel, rules, track record, alerts; HTTP + SSE) + Vue 3 frontend, one SQLite database. Public data only — no account access, no orders.

@AGENTS.md

## Claude Code specifics

- Reply to the owner in Chinese; write everything in the repository in English.
- Project skills:
  - `ctg-dev` — build, run, verify, gotchas (use for any code change).
  - `ctg-worktree` — parallel development: claim an issue, worktree, PR, self-review, merge, cleanup.
  - `ctg-issue` — file requirements and bugs, maintain the roadmap.
- Production: https://ctg.gugenzzz.top. Merging to `main` deploys through GitHub Actions (`gh run watch`); `deploy/deploy.sh` is for manual deploys from an up-to-date `main` only (see [operations](docs/operations.md)).
- Self-review before merging with `/code-review` on the branch, and post the result on the PR.
