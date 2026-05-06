# Plan 0001 — Scaffold the repo and capture the design conversation

**Status:** completed (2026-05-06)
**Opened:** 2026-05-06
**Closed:** 2026-05-06
**Owner:** initial author + assistant

## Goal

Stand up `livepeer-network-rewrite` as a docs-and-spec repository following the
agent-first harness pattern, and lock in the architectural conversation that motivated
its creation as a permanent reference.

## Why

The conversation captured a complete architectural sketch (8 layers, 11 requirements, a
proposed spec repo) plus the operator-voice rationale behind it. Without a repo to hold
that material, it would degrade into half-remembered chat. The harness pattern says
"the repo is the system of record" — this plan establishes the record.

## Outcomes

- [x] Repo exists at `/home/mazup/git-repos/livepeer-cloud-spe/livepeer-network-rewrite/`.
- [x] `AGENTS.md`, `README.md`, `CLAUDE.md`, `DESIGN.md`, `PRODUCT_SENSE.md`, `PLANS.md`
  at the root.
- [x] `docs/design-docs/` populated with `index.md`, `core-beliefs.md`,
  `requirements.md`, `architecture-overview.md`. Stub design-docs listed in `index.md`.
- [x] `docs/exec-plans/active/` and `docs/exec-plans/completed/` created.
- [x] `docs/exec-plans/tech-debt-tracker.md` created (empty header).
- [x] `docs/references/openai-harness.pdf` in place.
- [x] `docs/references/2026-05-06-architecture-conversation.md` captures the full
  conversation as point-in-time provenance.
- [x] `docs/product-specs/` and `docs/generated/` created (empty).

## Out of scope

- Writing any production code (no broker implementation, no gateway adapter).
- Creating the `livepeer-network-protocol` spec repo (that's plan 0002).
- Modifying any existing `livepeer-network-suite` submodule.

## Done condition

A new agent (or a returning human) opening the repo at `AGENTS.md` can navigate to the
full architectural intent in three clicks: `AGENTS.md` →
`docs/design-docs/architecture-overview.md` →
`docs/references/2026-05-06-architecture-conversation.md`. The conversation reference is
detailed enough to reconstruct every design decision without consulting the chat log.

## Closing notes

- All scaffold files landed and verified.
- Repo-shape decision recorded mid-plan: this repo is a **monorepo** for all components
  in the rewrite; subfolders per component will land as work progresses; components can
  be extracted to standalone repos later.
- Plan 0002's "spec repo location" question was resolved as a side-effect of the monorepo
  decision: the spec lives at `<repo-root>/livepeer-network-protocol/`.

## Follow-on

Plan 0002 (define interaction modes as specs) is now active.
