# Contributing

Conventions for everyone working on this repository — people and AI agents alike. The step-by-step flow for parallel work is in [worktree workflow](worktree-workflow.md).

## Language

- **English** for everything in the repository and on GitHub: documentation, code comments, commit messages, issues, pull requests and review comments.
- User-facing UI strings stay Chinese until [#5](https://github.com/genguzzz/crypto-trader-go/issues/5) (i18n) lands; new strings then go through the translation files.

## Requirements

A requirement is a GitHub issue created from the **Requirement** template, labeled `requirement`.

- Describe the **what and why**, never the implementation: summary, why, goals, non-goals, acceptance criteria, open questions, dependencies.
- Acceptance criteria are observable checks ("switching language updates every page"), not tasks ("add vue-i18n").
- One capability per issue. Split anything that can't ship in a few pull requests.
- Label it with one priority (`P1` next up, `P2` soon, `P3` later), one or more `area: …` labels and a status:

| Status | Meaning |
|---|---|
| `status: needs design` | scope or design questions must be settled before coding |
| `status: ready` | specified and unclaimed; anyone may claim it |
| `status: in progress` | claimed; a branch and pull request are being worked on |
| `status: blocked` | waiting on another issue or a decision |

- Add it to the pinned [Roadmap issue](https://github.com/genguzzz/crypto-trader-go/issues/6).
- Design for larger work goes into an issue comment (or `docs/design/<issue>-<slug>.md` when it's long) and is agreed before `status: ready`.
- Changing a definition (pool rules, field formulas, scoring semantics), adding a data source or touching account permissions needs the owner's confirmation.

## Bugs

A bug is an issue created from the **Bug** template, labeled `bug` plus an area and a severity-based priority:

| Severity | Examples | Priority |
|---|---|---|
| S1 | production down, data loss or corruption, secrets exposed | `P1`, fix immediately |
| S2 | wrong numbers, a broken main feature, a phone layout that is unusable | `P1`/`P2` |
| S3 | cosmetic issues, rare edge cases, workarounds exist | `P3` |

A good report has the steps to reproduce, expected vs actual behavior, the environment (production or local, device/browser, commit) and evidence (screenshot, log lines, API response). The fix PR adds a regression test when the bug is testable.

## Claiming work

1. Pick an issue labeled `status: ready` with no assignee.
2. Claim it: assign yourself, swap the status label, and comment with the branch name so parallel agents on the same account can see who holds it:
   ```bash
   gh issue edit 5 --add-assignee @me --remove-label "status: ready" --add-label "status: in progress"
   gh issue comment 5 --body "Claimed: branch feat/5-i18n"
   ```
3. Open a draft pull request early if the work spans several sessions.
4. To give an issue back, remove yourself and restore `status: ready` with a comment on where you stopped.

## Branches

`<type>/<issue>-<slug>`, e.g. `feat/5-i18n`, `fix/12-funding-sign`, `docs/7-api-guide`. The type uses the commit types below. Always branch from the latest `origin/main`.

## Commits

[Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <summary>

<body: why the change was needed and what changed, wrapped at ~72 columns>

Refs #12
```

- **type**: `feat`, `fix`, `perf`, `refactor`, `docs`, `test`, `build`, `ci`, `chore`, `style`, `revert`.
- **scope** (optional): the area touched — `scanner`, `chart`, `web`, `alerts`, `engine`, `features`, `rules`, `track`, `store`, `api`, `okx`, `deploy`, `docs`.
- **summary**: imperative, lowercase, no trailing period, ≤ 72 characters.
- One logical change per commit; tests and docs travel with the code they cover.
- Reference the issue in the body (`Refs #12`); the pull request, not the commit, closes it.
- Agents add a `Co-Authored-By:` trailer.
- Never commit credentials, `data/`, databases or build output. Never force-push `main`; rebase feature branches instead of merging `main` into them.

## Pull requests

- Title in commit format — it becomes the squash commit on `main` (`feat(i18n): English and Chinese UI`).
- Body follows the template: summary, `Closes #N`, changes, verification, notes (risks, schema changes, follow-ups).
- Keep it focused on one issue. Rebase on `origin/main` before asking for review.
- Before review: `make test` passes, the server was restarted from the branch and checked in a browser on desktop and phone (`scripts/dev.sh shots`), docs and `docs/roadmap.md` are updated.
- **Review**: the author (or the author's agent) reviews its own diff before merging — for Claude Code, run `/code-review` on the branch — and posts the result as a PR comment: findings and what was fixed, or "no findings". Fix and re-review until clean. GitHub doesn't let authors approve their own PRs; the owner is admin and merges directly.
- **Merge**: `gh pr merge <n> --squash --delete-branch`. Merging into `main` deploys to production through GitHub Actions (docs-only changes are skipped); follow it with `gh run watch` and check https://ctg.gugenzzz.top.
- Database schema changes must be additive (new tables/columns, no destructive migrations) because every environment upgrades its existing database in place.

## Definition of done

- Acceptance criteria of the issue are met and checked.
- `make test` passes; new logic has tests.
- The UI follows the [design guidelines](design-guidelines.md) and was verified on desktop and phone.
- Docs reflect the change ([architecture](architecture.md) for new data or APIs, [operations](operations.md) for new flags or deploy steps).
- `docs/roadmap.md` has a Shipped row; the issue is closed by the merged PR.
