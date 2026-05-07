# livepeer-network-rewrite

A workload-agnostic rearchitecture of the Cloud-SPE Livepeer Network supply side.

> **For agents:** start at [`AGENTS.md`](./AGENTS.md). The README below is a human-oriented overview.

## What this is

The current suite (`livepeer-network-suite`) ships three workload-shaped worker binaries
(`openai-worker-node`, `vtuber-worker-node`, `video-worker-node`) that each implement a
fixed set of capabilities at build time. Adding a brand-new capability type requires forking
a worker, modifying `worker-runtime`, coordinating a `livepeer-modules-project` release,
and editing the orch-coordinator. That coupling is the problem this repo exists to solve.

This repo's target is **a single workload-agnostic capability broker** that:

- Owns one host's `/registry/offerings`.
- Reads a single declarative `host-config.yaml`.
- Dispatches paid HTTP/streaming/RTMP/session traffic to arbitrary backends (local
  containers, LAN services, third-party APIs).
- Carries no per-capability code — only a small fixed typology of *interaction modes*.

The orchestrator's day-to-day surface becomes three steps with no code: **define**
capabilities + price, **identify** the servers, **serve**.

The full architectural rationale lives in
[`docs/references/2026-05-06-architecture-conversation.md`](./docs/references/2026-05-06-architecture-conversation.md).

## Status

Pre-implementation. The repo is a docs-and-spec scaffold; no production code yet.

- [`docs/design-docs/`](./docs/design-docs/) — what we believe and why
- [`docs/exec-plans/completed/`](./docs/exec-plans/completed/) — what has shipped
- [`docs/references/`](./docs/references/) — source material (conversation transcripts, the
  OpenAI Harness PDF)

## Repo shape — monorepo for now

This repo is the home for **everything** in the rewrite. Each component lands as a
top-level subfolder with its own `AGENTS.md`, `docs/`, source, and tests. None exist
yet; they grow as work progresses.

Planned (not created yet):

- `livepeer-network-protocol/` — the spec repo (modes, extractors, schemas, conformance tests)
- `capability-broker/` — workload-agnostic worker process (name TBD)
- `payment-daemon/` — receiver + sender, decoupled from capability/work-unit enums
- `service-registry-daemon/` — manifest schema + resolver/publisher
- `orch-coordinator/` — capability-as-roster-entry UX
- `secure-orch-console/` — diff + one-click sign UX
- `gateway-adapters/` — per-mode middleware (Go and TS reference impls)

Components can be **extracted to standalone repos later** once they stabilize and have
independent release cadences. The monorepo isn't a permanent shape; it's the cheapest
way to keep cross-cutting design coherent during the rewrite.

Cross-cutting design lives at the repo root in `docs/`. Per-component design lives
**inside the component's own `docs/`** when that component arrives.

## Operating model

This repo follows the agent-first harness pattern documented in
[`docs/references/openai-harness.pdf`](./docs/references/openai-harness.pdf):

- **Humans steer; agents execute.** Intent is set by humans; tools and feedback loops do the rest.
- **The repo is the system of record.** If it isn't checked in, it doesn't exist.
- **Progressive disclosure.** `AGENTS.md` is a *map*, not a manual. Detail lives in `docs/`.
- **Enforce invariants, not implementations.** Constraints in lints/CI; choices in code.
- **Throughput over ceremony.** Short-lived PRs; fix-forward over block.

## Layout

```
.
├── AGENTS.md              # Entry-point map for coding agents
├── CLAUDE.md              # Stub pointing Claude Code at AGENTS.md
├── DESIGN.md              # Architectural overview at a glance
├── PRODUCT_SENSE.md       # What this is + who/why + anti-goals
├── PLANS.md               # Current state and what's in flight
├── README.md              # You are here
├── docs/                  # Cross-cutting (suite-wide) docs
│   ├── design-docs/       # start at index.md
│   ├── exec-plans/        # active/, completed/, tech-debt-tracker.md
│   ├── product-specs/     # Cross-cutting feature specs (TBD)
│   ├── generated/         # Machine-produced reference (dep graphs, SBOMs)
│   └── references/        # External material (conversation transcripts, PDFs)
└── <component-name>/      # One subfolder per component (none yet — see "Repo shape")
    ├── AGENTS.md
    ├── docs/
    └── ...
```
