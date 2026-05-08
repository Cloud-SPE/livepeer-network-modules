---
id: 0006
slug: breaking-core-belief-direct-manifest-url
title: "[breaking] Update resolver compatibility invariant for direct manifest URLs"
status: completed
owner: agent
opened: 2026-05-01
closed: 2026-05-01
depends-on: 0005
---

## Goal

Update `docs/design-docs/core-beliefs.md` so the durable invariant matches the resolver contract now implemented in plan 0005: resolver deployments target one explicit registry contract and treat the on-chain `serviceURI` as the exact manifest URL to fetch.

Completion means the old base-URL plus derived-well-known-path language is removed from the invariant, and the surrounding resolver-facing docs remain coherent with that updated invariant.

## Why this plan exists

`core-beliefs.md` currently states that the on-chain string remains a plain URL and that structured metadata lives at a well-known path the URL points to. That was the old resolver contract. Plan 0005 changed resolver behavior to fetch the exact on-chain URL directly, so leaving the invariant untouched would keep the repo's protected architectural guidance out of sync with shipped code.

Because `core-beliefs.md` is explicitly protected, this change needs its own breaking plan and explicit human approval before edit.

## Non-goals

- No new resolver code changes.
- No publisher behavior changes.
- No changes to protocol-daemon publication behavior.
- No attempt to reopen multi-target resolution.

## Approach

### §A. Protected invariant update

- [x] Rewrite the backward-compatibility invariant in `docs/design-docs/core-beliefs.md` to describe exact-manifest-URL resolver semantics.
- [x] Preserve the workload-agnostic, provider-boundary, and signed-manifest trust invariants unchanged.

### §B. Tight doc reconciliation

- [x] Reconcile any immediately adjacent resolver docs whose wording would directly contradict the updated invariant.
- [x] Leave broader historical/reference docs alone unless they become actively misleading for current guidance.

## Decisions log

### 2026-05-01 — The protected change is semantic, not editorial

This is not a wording cleanup. The old invariant promised a base URL whose well-known subpath the resolver derived. The shipped resolver now fetches the exact on-chain manifest URL. Updating the invariant is therefore a real architectural change and is tracked as `[breaking]`.

## Open questions

- None. The human explicitly approved changing the protected invariant.

## Acceptance gates

- `core-beliefs.md` no longer claims the resolver derives a well-known subpath from a base URL.
- The invariant clearly states exact-manifest-URL resolver semantics.
- Resolver-facing docs near the invariant do not immediately contradict it.

## Artifacts produced

- Updated `docs/design-docs/core-beliefs.md` invariant §4 to describe exact-manifest-URL resolver semantics.
- Breaking-plan audit record in `docs/exec-plans/completed/0006-breaking-core-belief-direct-manifest-url.md`.

## Follow-ups

- If the publisher-side/operator-side wording across the wider repo still leans on the old base-URL mental model, open a follow-up documentation sweep rather than broadening this protected-invariant change ad hoc.
