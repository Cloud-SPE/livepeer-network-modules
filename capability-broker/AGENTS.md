# AGENTS.md

This is `capability-broker/` — the Go reference implementation of the
workload-agnostic capability broker per the spec at
[`../livepeer-network-protocol/`](../livepeer-network-protocol/).

Component-local agent map. The repo-root [`../AGENTS.md`](../AGENTS.md) is the
cross-cutting map; this file scopes to broker-specific guidance.

## Operating principles

Inherited from the repo root (agent-first harness pattern). Plus:

- **The spec is the source of truth.** When code disagrees with
  `../livepeer-network-protocol/`, the spec wins and the code is wrong. File
  a plan under `docs/exec-plans/active/` to fix.
- **The conformance suite is the grader.** When you change behavior on a
  paid path, update the conformance fixtures in
  `../livepeer-network-protocol/conformance/fixtures/` in the same PR.

## Where to look

| Question | File |
|---|---|
| What is this component? | [`README.md`](./README.md) |
| Architectural overview | [`DESIGN.md`](./DESIGN.md) |
| Package layout + HTTP surface + dispatch flow | [`docs/design-docs/architecture.md`](./docs/design-docs/architecture.md) |
| Day-2 operations (state store, session + job knobs) | [`docs/operator-runbook.md`](./docs/operator-runbook.md) |
| Build / run / test gestures | [`Makefile`](./Makefile) |
| Example operator config | [`examples/host-config.example.yaml`](./examples/host-config.example.yaml) |
| The wire spec this implements | [`../livepeer-network-protocol/`](../livepeer-network-protocol/) |

## Doing work in this component

- **All gestures are Docker-first** (per repo-root core belief #15). Do not
  add steps that require a host Go install. Use `make build`, `make run`,
  `make test`.
- **Source layout follows the `internal/` tree** in
  [`docs/design-docs/architecture.md`](./docs/design-docs/architecture.md).
  Add new packages under `internal/` per that tree; do not export internal
  types unless they're part of an embedding API.
- **There are exactly two protocols**: `paid-job/v1` (`POST /v1/job`) and
  `paid-session/v1` (`/v1/session/*`). The v0 seven-mode interaction
  taxonomy and its `POST /v1/cap` dispatch surface were removed in 2026-08;
  do not reintroduce a mode axis. New capability shapes are expressed as
  axes on the existing protocols (`job.transports`, the `session` block),
  not as new drivers.
- **Extractors are paid-job only.** `work_unit.extractor` is required for
  `paid-job/v1` and rejected for `paid-session/v1`, whose usage arrives as
  runner-reported cumulative claims.
- **Headers are validated in middleware**, not in handlers. The
  `Livepeer-*` header pipeline is a middleware chain; handlers see only
  fully-validated requests.
- **Dependencies stay current** (per repo-root core belief #16). Bump Go
  base image, modules, and tools as part of the PR that uses them; don't let
  drift accumulate.

## What lives elsewhere

- The wire spec → `../livepeer-network-protocol/`.
- The conformance test image → `../livepeer-network-protocol/conformance/`.
- Cross-cutting design (8-layer overview, requirements) → `../docs/design-docs/`.
- Repo-wide exec plans → `../docs/exec-plans/active/`.
