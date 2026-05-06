# PLANS

Current state of work in this repo, plus pointers to active plans.

## Current state

**Phase 0** — docs-and-spec scaffold. No production code. The repo is a system of record
for the architectural intent that motivates the rewrite.

**Repo shape: monorepo for now.** All components live as top-level subfolders here;
extraction to standalone repos is a v2 concern. See [`README.md`](./README.md) §"Repo
shape" for the planned component list.

What exists:

- This scaffold (AGENTS.md / DESIGN.md / PRODUCT_SENSE.md / README.md / CLAUDE.md).
- Core beliefs, requirements, and an architecture-overview design-doc.
- Full conversation provenance in `docs/references/2026-05-06-architecture-conversation.md`.
- Reference: `docs/references/openai-harness.pdf` (the operating-model template).

What does not exist yet:

- Any code.
- The `livepeer-network-protocol/` subfolder (spec contents — planned outcome of plan 0002).
- A capability-broker reference implementation.
- Any change to the existing `livepeer-network-suite`.

## Active plans

Numbered `docs/exec-plans/active/000N-*.md`. Currently:

- [`0002-define-interaction-modes-spec.md`](./docs/exec-plans/active/0002-define-interaction-modes-spec.md)
  — interaction-mode specs. **All decisions resolved; all artifacts landed
  except the gating done condition (broker reference impl passing conformance
  for one mode, addressed in plan 0003 + plan 0004).**
- [`0003-capability-broker.md`](./docs/exec-plans/active/0003-capability-broker.md)
  — Go reference implementation of the workload-agnostic broker. First
  milestone: end-to-end `http-reqresp` with `response-jsonpath` extractor and
  a mock payment client.

Completed plans live in [`docs/exec-plans/completed/`](./docs/exec-plans/completed/).

## Roadmap (rough; subject to change)

| Phase | Outcome | Component subfolder | Status |
|---|---|---|---|
| 0 | Docs-and-spec scaffold + conversation provenance | (root) | completed (plan 0001) |
| 1 | Interaction-mode specs published as a subfolder | `livepeer-network-protocol/` | mostly complete (plan 0002) |
| 2 | Capability-broker reference implementation (Go) | `capability-broker/` | in flight (plan 0003) |
| 2.5 | Conformance runner mode drivers | `livepeer-network-protocol/conformance/runner/` | queued (plan 0004) |
| 3 | Coordinator UX rework — capability-as-roster-entry | `orch-coordinator/` | not started |
| 4 | `payment-daemon` decoupling — opaque capability/work-unit names | `payment-daemon/` | not started (plan 0005 covers payment-daemon integration) |
| 5 | Gateway-side per-mode adapters | `gateway-adapters/` | not started (plan 0008) |
| 6 | Migration plan from existing suite — first adopter is the OpenAI-compat gateway | (root `docs/`) | not started (plan 0009) |

Phases 1–5 are independently shippable; phase 6 is gated on at least one
production gateway adopting the new shape. Components can be extracted from
this monorepo to standalone repos at any phase boundary.

## Versioning

Pre-1.0.0 until the first release is cut. **v1.0.0 = the first release of this
monorepo.** Components inside the monorepo do not have independent versions yet; when
a component is extracted to a standalone repo, its versioning becomes its own concern.
Until extraction, the monorepo's tag is the single coordinated release artifact for
everything in it.

This repo's release line is **independent of `livepeer-network-suite`**. The two share
no submodules, no pinned SHAs, and no schedule. See core belief #14.

## Tracking debt

[`docs/exec-plans/tech-debt-tracker.md`](./docs/exec-plans/tech-debt-tracker.md). Empty
at scaffold time; append as debt accumulates.
