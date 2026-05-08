# PLANS — how work is planned in this repo

Plans are first-class artifacts. They are versioned in-repo alongside code so agents can read progress and decision history from the repository itself.

## Two kinds of plans

### Ephemeral plans

For small, self-contained changes (< ~50 LOC, single domain, no schema/protocol change). Written inline in the PR description. No file created.

### Exec-plans

For complex work: multi-domain, schema change, protocol change, or anything an agent might pause mid-implementation and resume on later. Lives in `docs/exec-plans/active/`.

## Exec-plan file layout

```
docs/exec-plans/active/
├── 0001-<slug>.md      # in-flight
├── 0002-<slug>.md      # in-flight
docs/exec-plans/completed/
├── 0001-<slug>.md      # archived on merge
docs/exec-plans/tech-debt-tracker.md
```

IDs are monotonic, zero-padded to 4 digits.

## Exec-plan template

```markdown
---
id: 0001
slug: repo-scaffold
title: Stand up repo scaffolding
status: active          # active | blocked | completed | abandoned
owner: <agent-or-human>
opened: YYYY-MM-DD
---

## Goal
One paragraph. What are we trying to achieve and why.

## Non-goals
What is explicitly NOT in this plan.

## Approach
Bullet list of steps. Check off as completed.

- [ ] Step 1
- [ ] Step 2

## Decisions log
Append-only. Each decision: date + one-paragraph rationale.

### YYYY-MM-DD — <short title>
Reason: …

## Open questions
Things we need to answer before or during implementation.

## Artifacts produced
Links to PRs, generated docs, schemas created.
```

## Lifecycle

1. **Opened** — file created in `active/`, status `active`.
2. **In progress** — steps checked off, decisions appended.
3. **Blocked** — status flipped to `blocked`, open-questions populated, escalated.
4. **Completed** — all steps checked; file moved from `active/` → `completed/`, status updated, final artifacts linked.
5. **Abandoned** — status flipped to `abandoned`, reason added to decisions log, file moved to `completed/`.

## Rules

- Never modify plans in `completed/`. History is immutable.
- Every PR that changes `internal/` must link to an exec-plan in its description (unless the change is ephemeral).
- Plans may reference design-docs; design-docs may not reference plans.
- `tech-debt-tracker.md` is append-only with strike-through when resolved.
