---
id: 0001
slug: repo-scaffold
title: Stand up initial repository scaffolding
status: completed
owner: agent
opened: 2026-04-25
closed: 2026-04-25
---

## Goal

Lay down the full scaffolding for `livepeer-service-registry`: directory structure, layer placeholders, lints, CI, initial design-docs, supporting tooling, and a working v1 implementation of the manifest pipeline (build / sign / verify / resolve / cache / overlay-merge / select). The repo should be self-contained — `make build && make test && make lint` should all pass on first run.

## Non-goals

- No production-grade chain integration. We use a minimal `go-ethereum` dependency for the keystore and signature primitives, but the on-chain `ServiceRegistry` ABI binding is a stub returning operator-supplied test data unless real RPC is configured.
- No HSM/KMS signer. V3 JSON keystore only.
- No streaming RPC. All RPCs unary in v1.
- No on-chain `setServiceURI` autoreplay or retry logic. Single-shot tx submission; caller decides retry.
- No fancy selection algorithm. `Select` does conjunctive filter + weight-sort.

## Approach

- [x] Root harness files (AGENTS.md, DESIGN.md, PLANS.md, PRODUCT_SENSE.md, README.md, .gitignore)
- [x] Directory tree (cmd, proto, internal/{types,config,repo,service,runtime,providers,utils}, lint, examples, docs)
- [x] Initial design-docs (index, core-beliefs, architecture, manifest-schema, signature-scheme, serviceuri-modes, static-overlay, resolver-cache, grpc-surface, workload-agnostic-strings)
- [x] Product specs (index, grpc-surface, manifest-contract, legacy-compat)
- [x] Operations docs (running-the-daemon)
- [x] Tech-debt tracker seeded
- [x] go.mod / Go module skeleton, package doc.go files
- [x] `internal/types/` — Manifest, Node, Capability, Model, ResolveMode, errors
- [x] `internal/config/` — daemon config, static-overlay loader, validation
- [x] `internal/providers/` — interfaces + production + fake impls
- [x] `internal/repo/` — manifestcache, audit
- [x] `internal/service/` — resolver, publisher, selection, legacy
- [x] `proto/` — proto definitions; generated stubs deferred to `make proto` (Go-native fallback in `internal/runtime/grpc/` lets `make build` work without buf)
- [x] `internal/runtime/` — gRPC server (Go-native) + lifecycle
- [x] `cmd/` — entry binary with mode gating
- [x] Lints (coverage-gate stub, doc-gardener, layer-check stub, no-unverified-manifest)
- [x] CI workflows (lint, test)
- [x] Dockerfile + compose.yaml
- [x] Makefile
- [x] Example: minimal-e2e (publisher + resolver + consumer)
- [x] Example: static-overlay-only (chain-free demo)
- [x] `make build && make test` green; `make lint` green for custom lints (golangci-lint requires installation)
- [x] On completion: move this plan to `completed/` and seed follow-up plans

## Decisions log

### 2026-04-25 — Module path `github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon`
Mirrors `livepeer-payment-library`; makes cross-references obvious for operators running both daemons.

### 2026-04-25 — BoltDB for cache + audit log
Same reasoning as payment-library: single-writer, embedded, zero deps. The cache hot path is small (low thousands of orchestrators). Provider interface allows future swap.

### 2026-04-25 — Reject the on-chain CSV proposal
See `docs/references/csv-proposal-review.md`. The on-chain `serviceURI` stays a single URL pointer to `/.well-known/livepeer-registry.json`. CSV mode is read-only for accommodation, never produced.

### 2026-04-25 — Workload-agnostic capability strings
The registry does not parse, route on, or interpret capability names. AI / transcoding / openai are all opaque strings to the daemon. Adding a new workload type requires zero code changes here.

### 2026-04-25 — Generated proto stubs are committed
We commit `proto/gen/...` so consumers can `go install ./...` without buf installed. `make proto` regenerates; CI checks staleness.

## Open questions

- Does the operator's HTTP server typically support serving `/.well-known/...`? (Yes — the on-chain `serviceURI` already points at an existing HTTP endpoint, so the operator already has an HTTP server. We just write a file the operator's server picks up.)
- Should the publisher write to a file watched by the operator's server, or expose its own HTTP listener? (File. We don't want to compete with the operator's TLS / port story. Documented in `docs/operations/running-the-daemon.md`.)
- License? — Resolved 2026-04-25: MIT. Top-level `LICENSE` file added in commit after this plan closed. Same license as go-livepeer so consumers can mix and match without a license-compatibility audit.

## Artifacts produced

- Initial scaffold (this commit) — directory tree, root harness (AGENTS / DESIGN / PLANS / PRODUCT_SENSE / README), full design-docs (architecture, manifest-schema, signature-scheme, serviceuri-modes, static-overlay, resolver-cache, grpc-surface, workload-agnostic-strings), product-specs (grpc-surface, manifest-contract, legacy-compat), operations guide, references (csv-proposal-review, capability-enum-mapping), generated/manifest-example.
- Working `cmd/livepeer-service-registry-daemon` binary (~13 MiB). `--help`, `--mode=resolver --dev`, `--mode=publisher --dev` all boot and shut down cleanly.
- Working `examples/minimal-e2e` runs end-to-end: signed manifest → on-chain pointer → fetch → verify → resolve → select-by-capability for AI and transcoding.
- 18 packages with tests; 72.1% total statement coverage.

## Follow-ups (open as new plans when work begins)

- `0002-grpc-wire-binding` — wire the proto-generated server interface to the existing Go-native handlers. Re-add `google.golang.org/grpc` to go.mod.
- `0003-delegated-signing` — hot/cold key delegation for publisher.
- `0004-coverage-gate-impl` — implement the coverage-gate lint with per-package thresholds + exemptions.
- `0005-layer-check-impl` — fuller `go vet` analyzer beyond depguard (sibling-import rules, etc.).
