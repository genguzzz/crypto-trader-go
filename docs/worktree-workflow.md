# Worktree workflow

How several people or agents work on the repository at the same time without stepping on each other: one issue → one branch → one git worktree → one local server → one pull request. Conventions referenced here are in [contributing](contributing.md).

## Why worktrees

Each worktree is a separate checkout of its own branch sharing one `.git`. Combined with `scripts/dev.sh`, each also gets its **own port and database copy**, so parallel servers never share SQLite files or fight over `:8686`.

| Checkout | Location | Port | Database |
|---|---|---|---|
| main clone | `crypto-trader-go/` | 8686 | `data/ctg.db` (long-running local recorder) |
| worktree | `../ctg-<slug>/` | 8687–8699 (`data/port`) | `data/ctg.db`, a snapshot of the main clone's database |

Each server records live data from OKX and uses ~200 MB of memory and some CPU; stop servers you are not using.

## 1. Claim the issue

```bash
gh issue view 5
gh issue edit 5 --add-assignee @me --remove-label "status: ready" --add-label "status: in progress"
gh issue comment 5 --body "Claimed: branch feat/5-i18n"
```

## 2. Create the worktree

From the main clone:

```bash
scripts/dev.sh wt-new feat/5-i18n
```

This fetches `origin/main`, creates `../ctg-5-i18n` on the new branch, picks a free port, takes a consistent SQLite backup of the main database (safe while the main server runs), copies the icon cache and runs `npm ci`. Then:

```bash
cd ../ctg-5-i18n
scripts/dev.sh restart        # build web + binary, start on this worktree's port, wait until live (~1 min)
```

Without a main database the server bootstraps from scratch (~4 min to live; history backfills in the background).

## 3. Develop

- Commit early and often in [Conventional Commits](contributing.md#commits) format.
- Stay current: `git fetch origin && git rebase origin/main` (never merge `main` into the branch).
- Verify after every change:
  ```bash
  make test
  scripts/dev.sh restart
  scripts/dev.sh shots          # desktop + phone screenshots in data/shots/, fails on page errors or phone overflow
  ```
- Keep changes inside the issue's scope; file new issues for anything else you notice.
- Schema changes must be additive; note them in the pull request.

## 4. Open the pull request

```bash
git fetch origin && git rebase origin/main && make test
git push -u origin feat/5-i18n
cp .github/pull_request_template.md data/pr.md   # fill it in (data/ is ignored)
gh pr create --title "feat(i18n): English and Chinese UI" --body-file data/pr.md
```

The body contains `Closes #5`. Add `--draft` while work is incomplete.

## 5. Self-review

1. Review the diff against `origin/main` — in Claude Code run `/code-review` (or `/code-review high`) from the worktree.
2. Fix what it finds, rerun `make test`, push.
3. Post the outcome on the PR, e.g.:
   ```bash
   gh pr comment --body "Self-review (code-review high): 2 findings fixed in 3f2a1c0 (nil check in liquidation sync, missing phone style); no remaining findings. make test passes; screenshots checked on desktop and phone."
   ```

## 6. Merge and deploy

```bash
gh pr ready                                  # if it was a draft
gh pr merge --squash --delete-branch
gh run watch                                 # deploy workflow: tests → build → hot swap → health check
```

Then check https://ctg.gugenzzz.top. If the deploy fails the script rolls back automatically; fix forward with a new PR. Don't also run `deploy/deploy.sh` for the same change — that would race the workflow.

## 7. Clean up

From the main clone:

```bash
scripts/dev.sh wt-rm feat/5-i18n             # stops the worktree's server, removes the worktree and local branch
git pull --rebase                            # bring the main clone up to date
scripts/dev.sh restart                       # optional: run the new main locally
```

The issue closes automatically through `Closes #5`; make sure its Roadmap entry reflects that.

## Conflicts between parallel branches

- Whoever merges second rebases on the new `main`, resolves conflicts, reruns `make test` and the browser check, and force-pushes **their own feature branch only** (`git push --force-with-lease`).
- Files that commonly collide: `docs/roadmap.md` (append rows; keep both), `internal/api/api.go` routes, `web/src/api.ts` types, `internal/store/store.go` schema. Keep additions in these files small and grouped.

## Direct commits to main

Only the owner may ask for a direct commit to `main` (e.g. repository housekeeping). Even then: `make test`, `git pull --rebase`, then `git push` — never force.
