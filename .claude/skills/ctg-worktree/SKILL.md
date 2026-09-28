---
name: ctg-worktree
description: Parallel development in crypto-trader-go — claim an issue, create an isolated git worktree (own port and database copy), open a pull request, self-review with /code-review, squash-merge and clean up. Use when starting work on an issue, when several agents work at once, or when asked to open/merge a PR.
---

# ctg-worktree

Full rationale: `docs/worktree-workflow.md`; conventions: `docs/contributing.md`. One issue → one branch → one worktree → one server → one PR.

## 1. Claim

```bash
gh issue view <n>                                  # must be "status: ready" and unassigned
gh issue edit <n> --add-assignee @me --remove-label "status: ready" --add-label "status: in progress"
gh issue comment <n> --body "Claimed: branch <type>/<n>-<slug>"
```

If the issue is "needs design", settle the design with the owner first (comment on the issue) instead of coding.

## 2. Worktree (run from the main clone)

```bash
scripts/dev.sh wt-new <type>/<n>-<slug>            # → ../ctg-<n>-<slug>, free port 8687–8699, DB snapshot, npm ci
cd ../ctg-<n>-<slug> && scripts/dev.sh restart
```

Work only inside that directory. Use the `ctg-dev` skill for build/test/verify.

## 3. Develop

- Conventional Commits; `Refs #<n>` in bodies.
- `git fetch origin && git rebase origin/main` regularly; never merge main into the branch.
- `make test` → `scripts/dev.sh restart` → `scripts/dev.sh shots` → look at the images.
- Update docs and add a `docs/roadmap.md` Shipped row in the same branch.

## 4. Pull request

```bash
git fetch origin && git rebase origin/main && make test
git push -u origin HEAD
cp .github/pull_request_template.md data/pr.md    # fill in: summary, Closes #<n>, changes, verification
gh pr create --title "<type>(<scope>): <summary>" --body-file data/pr.md
```

## 5. Self-review (required)

1. Run `/code-review high` in the worktree (reviews the branch diff).
2. Fix findings, `make test`, push; repeat until no findings remain.
3. `gh pr comment --body "Self-review (code-review high): <findings and fixes, or no findings>. make test passes; desktop and phone checked."`

## 6. Merge

The owner is a GitHub admin and authorized agents to merge their own reviewed PRs (authors can't approve their own PRs on GitHub).

```bash
gh pr merge --squash --delete-branch
gh run watch                                       # deploy workflow; docs-only merges don't deploy
```

Verify https://ctg.gugenzzz.top. Don't also run `deploy/deploy.sh`. On a failed deploy (auto-rolled back), fix forward in a new PR.

## 7. Clean up (from the main clone)

```bash
scripts/dev.sh wt-rm <type>/<n>-<slug>
git pull --rebase
```

Confirm the issue closed and the Roadmap issue (#6) reflects it.

## Conflicts

The later PR rebases on the new main, reruns tests and the browser check, then `git push --force-with-lease` on its own branch only. Hot spots: `docs/roadmap.md` (keep both rows), `internal/api/api.go`, `web/src/api.ts`, `internal/store/store.go`. Schema changes must be additive.
