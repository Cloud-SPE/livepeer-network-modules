# PLANS

Module-internal exec-plans for protocol-daemon. The cross-module plan that birthed this daemon is [`docs/exec-plans/completed/0006-build-protocol-daemon.md`](../docs/exec-plans/completed/0006-build-protocol-daemon.md) (lives in the monorepo root, completed when the daemon shipped).

## Active plans

(none)

## Completed plans (cross-module, monorepo-root)

- [`0014` — bind the unix socket and serve the gRPC surface](../docs/exec-plans/completed/0014-protocol-daemon-grpc-listener.md) (2026-04-29; shipped as monorepo `v2.0.1`).
- [`0015` — explicit skip semantics for force-actions (ForceOutcome)](../docs/exec-plans/completed/0015-protocol-daemon-force-outcome.md) (2026-04-29; shipped as monorepo `v2.1.0`, wire-breaking).

## Completed plans

Module-internal plans land in [`docs/exec-plans/completed/`](docs/exec-plans/completed/) when complete; the monorepo-level plan lives at the root of the monorepo.

## Tech debt

Tracked alongside the plans inventory in `docs/exec-plans/`. (No `tech-debt-tracker.md` aggregator file yet; create one when the backlog grows.)
