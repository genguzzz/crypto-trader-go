---
name: ctg-issue
description: File and maintain crypto-trader-go GitHub issues — requirements (what/why, no implementation), bugs (repro, severity), labels, and the pinned Roadmap issue. Use when the owner describes a new feature idea or a problem, asks to plan work, or asks what is on the roadmap.
---

# ctg-issue

Conventions: `docs/contributing.md` (#requirements, #bugs). Everything on GitHub is written in English; polish the owner's wording into clear requirements.

## Requirement

1. Check for duplicates: `gh issue list --search "<keywords>" --state all`.
2. Write the body from `.github/ISSUE_TEMPLATE/requirement.md`: Summary, Why, Goals, Non-goals, Acceptance criteria (observable checks), Open questions, Dependencies. **No implementation details.** One capability per issue.
3. Create with labels: `requirement`, one of `P1|P2|P3`, `area: …`, and `status: needs design` (or `status: ready` when fully specified, `status: blocked` when it depends on an open issue).
   ```bash
   gh issue create --title "<capability>" --body-file data/issue.md --label "requirement,P2,area: web,status: needs design"
   ```
4. Add it to the Roadmap issue (#6) under the matching priority section (`gh issue view 6 --json body -q .body > data/roadmap.md`, edit, `gh issue edit 6 --body-file data/roadmap.md`) and to the Planned table in `docs/roadmap.md`.
5. Changing definitions, adding data sources or account access: note "needs owner confirmation" in Open questions and ask the owner.

## Bug

1. Reproduce first (local server via `ctg-dev`, or production). Capture evidence: screenshot, `data/server.log` / `deploy/deploy.sh --status`, API response. Never include secrets.
2. Body from `.github/ISSUE_TEMPLATE/bug.md`; severity S1 → `P1` (fix now), S2 → `P1`/`P2`, S3 → `P3`.
3. Labels: `bug`, priority, `area: …`.

## Labels

`requirement`, `bug`, `roadmap`; `P1` `P2` `P3`; `status: needs design|ready|in progress|blocked`; `area: web|engine|alerts|strategy|auth|agent|i18n`. Create a new area label only when none fits (`gh label create "area: x" --color bfdadc`).

## Roadmap upkeep

- The pinned issue #6 is the plan; `docs/roadmap.md` holds the Planned table and the Shipped history.
- Status lives in labels; keep the Roadmap sections (Now / Next / Later / Backlog) consistent with priorities.
- Backlog bullets become real issues before anyone starts on them.
