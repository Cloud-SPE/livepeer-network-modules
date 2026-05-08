# Design docs index

A catalog of every design-doc in this repo, with verification status and core beliefs.

## Verification status

Each doc carries a `status:` field in its frontmatter. Values:

| Status | Meaning |
|---|---|
| `proposed` | Written, not yet reviewed or implemented |
| `accepted` | Reviewed, intended direction, not yet fully implemented |
| `verified` | Implemented and matches code; covered by tests |
| `deprecated` | Superseded or abandoned; kept for history |

A doc-gardening lint in CI flags docs with stale status, broken cross-links, or no recent touch after linked code last changed.

## Core beliefs

Non-negotiables that shape every decision in this repo.

- [core-beliefs.md](core-beliefs.md) — `accepted`

## Architectural decisions

- [architecture.md](architecture.md) — `verified` — layer stack, domains, providers (boundaries enforced by golangci-lint depguard in `.golangci.yml`)
- [manifest-schema.md](manifest-schema.md) — `accepted` — the JSON schema served at the exact manifest URL published on-chain, including canonical-bytes definition for signing (covered by `internal/types/decoder_test.go`)
- [signature-scheme.md](signature-scheme.md) — `verified` — Ethereum personal-sign over canonical bytes; recover-then-compare against the chain-claimed eth address (covered by `internal/providers/{signer,verifier}/`)
- [serviceuri-modes.md](serviceuri-modes.md) — `accepted` — resolver interpretation of full manifest URLs, CSV-fallback (read-only), and chainless static-overlay synth (covered by `internal/service/resolver/`)
- [static-overlay.md](static-overlay.md) — `verified` — operator-curated `nodes.yaml` overlay rules and merge precedence (covered by `internal/config/overlay_test.go` + resolver overlay tests)
- [resolver-cache.md](resolver-cache.md) — `verified` — TTL, last-good fallback, audit-event log (covered by `internal/repo/{manifestcache,audit}/`)
- [grpc-surface.md](grpc-surface.md) — `verified` — gRPC contract bound to a unix-socket *grpc.Server (covered by `internal/runtime/grpc/wire_test.go`)
- [observability.md](observability.md) — `verified` — Prometheus metric catalog, label enums, cardinality philosophy, sample queries (covered by `internal/providers/metrics/` + `internal/runtime/metrics/`)
- [workload-agnostic-strings.md](workload-agnostic-strings.md) — `accepted` — why capability strings are opaque, naming conventions for known workloads
- [worker-offerings-endpoint.md](worker-offerings-endpoint.md) — `verified` — uniform `/registry/offerings` HTTP convention every worker exposes for orch-coordinator scrape; body shape + optional bearer auth
- [adding-a-new-workload.md](adding-a-new-workload.md) — `verified` — onramp recipe for new workload authors; capability naming, work_unit choice, pricing pattern, worker template, archetype-A integration

## Conventions

- Every design-doc has frontmatter: `title`, `status`, `last-reviewed`, optional `supersedes` and `superseded-by`.
- Docs may link to other docs; they may not link into `exec-plans/` (plans are transient; docs are durable).
- When implementation diverges from a doc, either the code changes to match or the doc is updated — never both out of sync.
