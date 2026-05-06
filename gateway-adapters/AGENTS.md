# AGENTS.md

This is `gateway-adapters/` — the TypeScript reference middleware for the
gateway-side wire protocol per
[`../livepeer-network-protocol/`](../livepeer-network-protocol/). Distributed
as `@tztcloud/livepeer-gateway-middleware`.

Component-local agent map. The repo-root [`../AGENTS.md`](../AGENTS.md) is the
cross-cutting map; this file scopes to gateway-adapters-specific guidance.

## Operating principles

Inherited from the repo root. Plus:

- **Library code, not a service.** The gateway operator imports this and
  wires it into their request pipeline. The package ships as npm; its
  Dockerfile is for the test/build environment, not for production
  deployment (per core belief #15 — services ship as images, libraries
  ship as packages).
- **Zero runtime dependencies.** This package depends only on Node's
  built-in modules (`node:fetch`, `node:undici`, `node:http`). Adding
  any runtime dependency requires a planned exec-plan.
- **The spec is the source of truth.** When code disagrees with
  `../livepeer-network-protocol/`, the spec wins.

## Where to look

| Question | File |
|---|---|
| What is this component? | [`README.md`](./README.md) |
| Architectural overview | [`DESIGN.md`](./DESIGN.md) |
| Planned package layout | [`docs/design-docs/architecture.md`](./docs/design-docs/architecture.md) |
| Build / test gestures | [`Makefile`](./Makefile) |
| The wire spec this implements | [`../livepeer-network-protocol/`](../livepeer-network-protocol/) |
| The broker it talks to | [`../capability-broker/`](../capability-broker/) |

## Doing work in this component

- **All gestures are Docker-first** (per repo-root core belief #15). Use
  `make build`, `make test`. No host npm install required.
- **TypeScript with strict types.** `tsc` is the source of truth for
  type-checking; `tsconfig.json` enables `strict` mode.
- **Tests use `node:test`** (Node's built-in test runner) — no jest, no
  vitest, no mocha.
- **Dependencies stay current** (per repo-root core belief #16).

## What lives elsewhere

- The wire spec → `../livepeer-network-protocol/`.
- The broker reference impl → `../capability-broker/`.
- Cross-cutting design → `../docs/design-docs/`.
- Repo-wide exec plans → `../docs/exec-plans/active/`.
