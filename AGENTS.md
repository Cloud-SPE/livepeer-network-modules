# AGENTS.md

This is `livepeer-network-rewrite` — a docs-and-spec scaffold for a workload-agnostic
rearchitecture of the Cloud-SPE Livepeer Network supply side. **No production code yet.**

## Operating principles

This repo follows the agent-first harness pattern in
[`docs/references/openai-harness.pdf`](./docs/references/openai-harness.pdf). Short version:

- **You steer; the agent executes.** Humans set intent; tools and feedback loops do the rest.
- **The repo is the system of record.** If it isn't checked in, it doesn't exist.
- **Progressive disclosure.** This file is a *map*, not a manual.
- **Enforce invariants, not implementations.** Constraints in lints/CI; choices in code.
- **Throughput over ceremony.** Short-lived PRs; fix-forward over block.

Read [`docs/design-docs/core-beliefs.md`](./docs/design-docs/core-beliefs.md) before making
load-bearing decisions.

## Where to look

| Question | File |
|---|---|
| What is this repo and why does it exist? | [`README.md`](./README.md) |
| What invariants must any change uphold? | [`docs/design-docs/core-beliefs.md`](./docs/design-docs/core-beliefs.md) |
| What are the supply-side requirements we're designing against? | [`docs/design-docs/requirements.md`](./docs/design-docs/requirements.md) |
| What's the proposed architecture at a glance? | [`docs/design-docs/architecture-overview.md`](./docs/design-docs/architecture-overview.md) |
| What design work is in flight? | [`docs/exec-plans/active/`](./docs/exec-plans/active/) |
| What design work has shipped? | [`docs/exec-plans/completed/`](./docs/exec-plans/completed/) |
| What known tech debt are we tracking? | [`docs/exec-plans/tech-debt-tracker.md`](./docs/exec-plans/tech-debt-tracker.md) |
| What's the source-of-truth for the design conversation? | [`docs/references/2026-05-06-architecture-conversation.md`](./docs/references/2026-05-06-architecture-conversation.md) |
| Reference material (papers, PDFs, transcripts) | [`docs/references/`](./docs/references/) |

## Repo shape — monorepo for now

This repo is the home for **everything** in the rewrite. Components land as top-level
subfolders, each with their own `AGENTS.md`, `docs/`, source, and tests. Components can
be extracted to standalone repos later once they stabilize.

When a component subfolder exists, navigate from this `AGENTS.md` to that component's
own `AGENTS.md` for component-specific guidance. The root-level `docs/` holds only
**cross-cutting** material that binds more than one component.

Planned components (none created yet): `livepeer-network-protocol/`,
`capability-broker/`, `payment-daemon/`, `service-registry-daemon/`,
`orch-coordinator/`, `secure-orch-console/`, `gateway-adapters/`. See
[`README.md`](./README.md) §"Repo shape" for the full list.

## Doing work in this repo

- **Cross-cutting design lives in root `docs/design-docs/`.** Promote a doc here only
  when it binds more than one component. Component-local design lives in
  `<component>/docs/` once that component exists.
- **Cross-cutting plans go in root `docs/exec-plans/active/`.** Numbered (`0001-…`,
  `0002-…`). Move to `completed/` when shipped. Component-local plans live in
  `<component>/docs/exec-plans/`.
- **Conversations and external material go in `docs/references/`.** Date-stamped. Treated
  as point-in-time provenance — do not edit after the fact; supersede with a new doc if
  the picture changes.
- **No production code yet.** When code arrives, it goes in a top-level component
  subfolder.

## What lives elsewhere

- Implementation of the existing suite — `livepeer-network-suite` and its 14 submodules
  (sibling working tree, not vendored here).
- The dead `livepeer-modules-conventions` reference — replaced by the
  `livepeer-network-protocol/` subfolder (planned; see
  [`docs/design-docs/architecture-overview.md`](./docs/design-docs/architecture-overview.md)
  §6.11).

## Doc-gardening expectations

Stale docs are worse than missing docs. When you change a process or an invariant, update
the doc in the same PR. References (`docs/references/`) are point-in-time and do **not**
get edited after the fact — supersede with a new dated reference if the picture changes.
