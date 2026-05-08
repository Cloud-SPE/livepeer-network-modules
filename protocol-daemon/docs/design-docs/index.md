# Design docs index

Module-internal design documents for protocol-daemon. Cross-module concerns live in the monorepo root [`docs/design-docs/`](../../../docs/design-docs/).

## Catalog

| Doc | Status | Last reviewed |
|---|---|---|
| [`architecture.md`](architecture.md) — layer stack, providers boundary | accepted | 2026-04-26 |
| [`core-beliefs.md`](core-beliefs.md) — module-specific invariants | accepted | 2026-04-26 |
| [`roundinit-loop.md`](roundinit-loop.md) — round initialization sequence | accepted | 2026-04-26 |

Reward eligibility, positional-hints caching, and observability are documented inline (package doc-comments under `internal/service/reward/`, `internal/repo/poolhints/`, `internal/runtime/metrics/`); standalone design docs are tracked as a follow-up.

## How this fits with the monorepo

- Cross-module invariants in [`monorepo/docs/design-docs/core-beliefs.md`](../../../docs/design-docs/core-beliefs.md) apply to every module; this module's `core-beliefs.md` only adds module-specific clauses on top.
- Conventions for metrics naming and TCP ports live in [`monorepo/docs/conventions/`](../../../docs/conventions/).
- The chain-commons library this module consumes is documented at [`monorepo/docs/design-docs/chain-commons-api.md`](../../../docs/design-docs/chain-commons-api.md).
