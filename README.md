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

Implementation is underway. The repo now contains working component code alongside the
cross-cutting design docs.

- [`docs/design-docs/`](./docs/design-docs/) — what we believe and why
- [`docs/exec-plans/completed/`](./docs/exec-plans/completed/) — what has shipped
- [`docs/references/`](./docs/references/) — source material (conversation transcripts, the
  OpenAI Harness PDF)

## Setup (fresh clone)

The repo pins its Node and pnpm versions so installs are reproducible. Three
one-time commands after cloning:

```sh
# 1. Switch to the pinned Node major (reads .nvmrc).
#    Replace `fnm` with `nvm`/`asdf`/whichever manager you use.
fnm use

# 2. Enable Corepack — ships with Node, off by default. This makes the
#    pinned pnpm@9.0.0 available via shim. Re-run after switching Node
#    versions; not needed again after that.
corepack enable

# 3. Install workspace dependencies. With `engine-strict=true` in
#    .npmrc, this hard-fails if step 1 left you on the wrong Node.
pnpm install
```

You never run `npm install -g pnpm` — Corepack reads `packageManager` from
`package.json` and materializes the exact pinned pnpm release (with sha512
integrity hash). That removes a whole class of "works on my machine"
breakage where contributors run different pnpm versions.

## Repo shape — monorepo for now

This repo is the home for **everything** in the rewrite. Each component lands as a
top-level subfolder with its own `AGENTS.md`, `docs/`, source, and tests.

Current components:

- `livepeer-network-protocol/` — spec repo (modes, extractors, schemas, conformance)
- `capability-broker/` — workload-agnostic worker process
- `payment-daemon/` — receiver + sender, decoupled from capability/work-unit enums
- `orch-coordinator/` — manifest candidate builder + publisher host
- `secure-orch-console/` — cold-key diff-and-sign console
- `protocol-daemon/` — round init, reward, and on-chain service-URI daemon
- `service-registry-daemon/` — consumer-side resolver for on-chain orch discovery + manifest fetch/verify/cache
- `chain-commons/` — shared chain/RPC/txintent support used by protocol-daemon
- `proto-contracts/` — generated protobuf bindings shared by daemon surfaces
- `gateway-route-health/` — shared Layer 3 route-health tracker used by gateways

Planned or still-expanding areas:

- `gateway-adapters/` — per-mode middleware (Go and TS reference impls)

Components can be **extracted to standalone repos later** once they stabilize and have
independent release cadences. The monorepo isn't a permanent shape; it's the cheapest
way to keep cross-cutting design coherent during the rewrite.

Cross-cutting design lives at the repo root in `docs/`. Per-component design lives
**inside the component's own `docs/`** when that component arrives.

## Workspace packages

The JS/TS parts of the repo use a root `pnpm` workspace. Shared packages such as
`customer-portal/`, `customer-portal/frontend/shared/`, and
`gateway-route-health/` are consumed via `workspace:*` dependencies by the
gateway packages.

When adding a new shared JS/TS package:

- add it to [`pnpm-workspace.yaml`](./pnpm-workspace.yaml)
- reference it from dependents with `workspace:*`
- run `pnpm install` so local workspace links are refreshed before building

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
└── <component-name>/      # One subfolder per component
    ├── AGENTS.md
    ├── docs/
    └── ...
```
