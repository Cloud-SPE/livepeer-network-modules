---
id: 0002
slug: grpc-wire-binding
title: Bind generated gRPC code to the Go-native handlers
status: completed
owner: agent
opened: 2026-04-25
closed: 2026-04-25
---

## Goal

Make the daemon production-ready by replacing the in-process Go-native handler surface with a real gRPC server bound to a unix socket. Generate proto stubs from the v1 contract, write thin adapters that delegate to the existing `internal/runtime/grpc.Server` methods, install structured logging + panic-recovery interceptors, register the standard gRPC health and reflection services, and add bufconn round-trip tests so a regression in the wire layer fails CI.

## Non-goals

- No schema changes. Anything that touches `proto/livepeer/registry/v1/*.proto` field numbers or methods is out of scope.
- No TLS / TCP listener. v1 is unix-socket-only per `docs/design-docs/core-beliefs.md` ("project-specific invariants"). TLS-over-TCP is its own future plan.
- No streaming RPCs. All v1 RPCs are unary.
- No gRPC client SDK. Consumers can generate their own from `proto/`; we don't ship a `clients/go/` package in this plan.

## Approach

- [x] Tooling: `buf` v2 module config (`proto/buf.yaml`, `buf.gen.yaml`) using BSR remote plugins for `protocolbuffers/go` + `grpc/go`. `make proto` runs `buf lint && buf generate`.
- [x] Run `make proto` to populate `proto/gen/go/livepeer/registry/v1/*.pb.go` + `*_grpc.pb.go`. Generated artifacts committed.
- [x] Re-add `google.golang.org/grpc@v1.80.0` and `google.golang.org/protobuf@v1.36.11` to `go.mod`.
- [x] `internal/runtime/grpc/convert.go` — bidirectional conversion + round-trip tests in `convert_test.go`.
- [x] `internal/runtime/grpc/status.go` — sentinel → gRPC code mapping; stable error code string carried as a `*structpb.Struct` detail (extractable via `status.FromError(err).Details()`); coverage in `status_test.go`.
- [x] `internal/runtime/grpc/resolver_adapter.go` — implements `registryv1.ResolverServer`.
- [x] `internal/runtime/grpc/publisher_adapter.go` — implements `registryv1.PublisherServer`. Uses the new `types.DecodeUnsignedManifest` boundary decoder (the only relaxed JSON entry point) so the `no-unverified-manifest` lint stays happy.
- [x] `internal/runtime/grpc/listener.go` — `NewListener` + `Serve` + idempotent `Stop`. Unary interceptors: panic recovery, per-RPC default deadline (30s), structured logging (debug for `health.Check`, warn for client errors, error for server errors). Standard gRPC health service + reflection. Unix socket binds with mode `0o600`; stale-socket cleanup refuses to remove non-socket files. GracefulStop has a 2s cap with abandon-and-exit fallback to handle keepalive-pinned conns.
- [x] Wired into `cmd/livepeer-service-registry-daemon/run.go` via `lifecycle.Run` with the new `Listener` field.
- [x] `internal/runtime/grpc/wire_test.go` — bufconn round-trip for ResolveByAddress (happy + not-found + bad-eth + signature_mismatch + cache reuse), Select, Refresh, ListKnown, GetAuditLog, Health, Publisher BuildSignWrite. Real unix-socket lifecycle test via `TestUnixSocketLifecycle`.
- [x] Smoke-tested against the actual binary: `examples/smoke-client/` connects to a running daemon over unix socket, hits Health and Resolver.Health, sees stable `not_found` registry code on missing addresses.
- [x] Updated `docs/design-docs/grpc-surface.md` status from `accepted` to `verified`. Bumped four other implementation-backed docs (manifest-schema, signature-scheme, serviceuri-modes, static-overlay, resolver-cache) to `verified` while we were here.
- [x] On completion: move plan to `completed/`, link artifacts.

## Decisions log

### 2026-04-25 — Use buf with BSR remote plugins
`buf generate` uses `buf.build/protocolbuffers/go` and `buf.build/grpc/go` so contributors don't need `protoc-gen-go-grpc` installed locally. `make proto` runs `buf lint && buf generate`. Locally-installed plugins remain a fallback for offline builds.

### 2026-04-25 — Generated proto code is committed
We commit `proto/gen/go/livepeer/registry/v1/*.pb.go` so `go install ./...` works without buf installed. `make proto` regenerates; CI checks staleness via `git diff --exit-code` after running it (queued for a follow-up plan; today's CI just runs `go test`).

### 2026-04-25 — Stable error-code detail uses *structpb.Struct, not custom proto
We use a `*structpb.Struct` with a single `registry_error_code` key as the gRPC status detail rather than defining a custom `RegistryError` message. The string codes are stable per `docs/product-specs/grpc-surface.md`; structpb survives across language clients without code-gen. Switching to a typed message later is non-breaking.

### 2026-04-25 — GracefulStop has a hard 2s cap, not a configurable timeout
grpc-go 1.80's `Server.Stop()` and `Server.GracefulStop()` contend on the same internal mutex; calling Stop concurrently with a pending GracefulStop deadlocks. We cap GracefulStop at 2s and abandon drain on timeout — the per-RPC deadline interceptor caps individual handler latency anyway, so in-flight RPCs can't run forever. Operators concerned about longer drains should adjust the per-RPC deadline first.

### 2026-04-25 — Unix-socket only; mode 0o600
Documented in core-beliefs.md; the listener `chmod 0o600` after binding so any user other than the daemon owner can't connect. Stale-socket cleanup at startup refuses to remove non-socket files (defends against the operator pointing `--socket` at an unrelated regular file).

## Artifacts produced

- Commit `72dd253` — Initial scaffold + gRPC wire binding (single foundational commit; this plan landed alongside `0001-repo-scaffold`).
- `proto/gen/go/livepeer/registry/v1/*.pb.go` + `*_grpc.pb.go` — generated bindings.
- `internal/runtime/grpc/convert.go,status.go,resolver_adapter.go,publisher_adapter.go,listener.go` and their tests.
- `examples/smoke-client/` — manual sanity tool.
- `docs/design-docs/grpc-surface.md` and four sibling design docs bumped to `verified`.
