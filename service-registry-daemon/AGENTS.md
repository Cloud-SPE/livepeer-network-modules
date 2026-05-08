# AGENTS.md — livepeer-service-registry

This is the Livepeer Service Registry Daemon repository. The daemon decouples orchestrator/worker discovery from `go-livepeer` by serving a small gRPC surface over a local unix socket. Any application — gateways, bridges, transcoding clients, AI-job dispatchers — can publish capabilities or resolve them without depending on `go-livepeer`'s monolithic discovery code paths.

**Humans steer. Agents execute. Scaffolding is the artifact.**

## Start here

- Design & domains: [DESIGN.md](DESIGN.md)
- How to plan work: [PLANS.md](PLANS.md)
- Product mental model: [PRODUCT_SENSE.md](PRODUCT_SENSE.md)
- Why the on-chain CSV proposal was rejected: [docs/references/csv-proposal-review.md](docs/references/csv-proposal-review.md)

## Knowledge base layout

- `docs/design-docs/` — catalogued design decisions (`index.md` is the entry)
- `docs/exec-plans/active/` — in-flight work with progress logs
- `docs/exec-plans/completed/` — archived plans; do not modify
- `docs/exec-plans/tech-debt-tracker.md` — known debt, append-only
- `docs/product-specs/` — gRPC contract and operator/consumer guarantees
- `docs/generated/` — auto-generated; never hand-edit
- `docs/operations/` — running-the-daemon guides
- `docs/references/` — external material (PDFs, prior-art reviews)

## The layer rule (non-negotiable)

Source under `internal/` follows a strict dependency stack:

```
types → config → repo → service → runtime
```

Cross-cutting concerns (Eth client, manifest HTTP fetcher, signer, store, clock, logger) enter through a single layer: `internal/providers/`. Nothing in `service/` may import `eth`, `bbolt`, `net/http`, etc. directly — only through a `providers/` interface.

Lints enforce this in CI. See [docs/design-docs/architecture.md](docs/design-docs/architecture.md).

## Toolchain

- Go 1.25+
- `buf` + `protoc` for `proto/` generation
- `golangci-lint` + custom lints in `lint/`

## Commands

- `make build` — build the daemon binary
- `make test` — run unit tests (race-enabled)
- `make lint` — run all lints (golangci-lint + custom)
- `make proto` — regenerate code from `.proto` files
- `make doc-lint` — validate knowledge-base cross-links and freshness

## Invariants (do not break without a design-doc)

1. **All resolver modes coexist.** Resolver must transparently handle: (a) a full manifest URL `serviceURI`, (b) an opt-in CSV pointer if encountered (read-only fallback), and (c) chainless static-overlay synth when the chain has no entry but the operator overlay supplies pins (`--discovery=overlay-only` deployments). See `docs/design-docs/serviceuri-modes.md`.
2. **Workload-agnostic.** No domain in `internal/` may hard-code "ai", "transcoding", "openai", "llm". Capabilities are opaque strings; the registry doesn't know what they mean. See core-beliefs §3.
3. **Providers boundary.** No cross-cutting dependency is imported outside `internal/providers/`.
4. **Manifests are signed.** Resolver rejects an unsigned manifest unless the operator has whitelisted that ethAddress as `unsigned-allowed` in the static overlay. Enforced by `lint/no-unverified-manifest`.
5. **No code without a plan.** Non-trivial work starts with an entry in `docs/exec-plans/active/`.
6. **Test coverage ≥ 75% per package.** CI fails below this threshold.

## Where to look for X

| Question | Go to |
|---|---|
| What does the daemon do? | [DESIGN.md](DESIGN.md) |
| How does information flow end-to-end? | `docs/design-docs/architecture.md#information-flow` (Mermaid diagrams) |
| Why is X done this way? | `docs/design-docs/` |
| What's in flight? | `docs/exec-plans/active/` |
| What's the manifest schema? | `docs/design-docs/manifest-schema.md` |
| How does legacy fallback work? | `docs/design-docs/serviceuri-modes.md` |
| What's the gRPC contract? | `docs/product-specs/grpc-surface.md` |
| What metrics are emitted? | `docs/design-docs/observability.md` |
| How do I deploy / run it? | `docs/operations/running-the-daemon.md` + the README's Docker / Quick start sections |
| Known debt? | `docs/exec-plans/tech-debt-tracker.md` |
