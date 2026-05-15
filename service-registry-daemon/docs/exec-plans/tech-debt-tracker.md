# Technical debt tracker

Append-only list of known debt. Strike through when resolved; include the PR or exec-plan that resolved it.

## Format

```
### <short-title>
- Opened: YYYY-MM-DD
- Severity: low | medium | high
- Area: <layer or domain>
- Description: one paragraph
- Remediation: link to exec-plan or "TODO"
- Resolved: <YYYY-MM-DD in PR #nnn>  (add on close; strike-through the title)
```

## Items

### layer-check-full-impl
- Opened: 2026-04-25
- Severity: medium
- Area: `lint/`
- Description: `lint/layer-check/` is a stub. The full implementation is a `go vet` analyzer that walks `internal/*` packages and rejects imports that violate the dependency rule from `docs/design-docs/architecture.md`. golangci-lint's `depguard` covers v1; the analyzer is a stricter follow-up that would catch service-sibling imports inside `internal/service/`.
- Remediation: open `0002-custom-lints` once the source tree has stabilized and we know concrete patterns to forbid.
- Resolved: —

### hsm-kms-signer
- Opened: 2026-04-25
- Severity: medium
- Area: `internal/providers/signer`
- Description: The default `Signer` decrypts the V3 JSON keystore and holds the secp256k1 private key in process memory until shutdown. Operators running serious orchestrator identities want HSM/KMS-backed signing.
- Remediation: add a `providers.Signer`-compatible provider that delegates to AWS KMS / GCP KMS / YubiHSM. The interface accepts `func(canonical []byte) (sig []byte, err error)`, so this is additive.
- Resolved: —

### hot-cold-delegation
- Opened: 2026-04-25
- Severity: medium
- Area: `internal/service/publisher`, `internal/service/resolver`
- Description: An operator's orchestrator identity is often a cold wallet. They don't want it loaded into the daemon. A delegation document signed by the cold key authorizing a hot key for the publisher would let the cold key stay offline.
- Remediation: define a `delegation` blob under `manifest.extra`, add `--allow-delegated-signing` to publisher, add chain-of-trust verification in resolver. Open `0003-delegated-signing`.
- Resolved: —

### manifest-streaming-updates
- Opened: 2026-04-25
- Severity: low
- Area: `internal/runtime/grpc`
- Description: Resolvers re-poll on TTL. A streaming RPC that pushes "manifest changed for X" would let a bridge react in <1s instead of waiting up to `--cache-manifest-ttl`. Adds connection-state complexity; deferred.
- Remediation: add `Resolver.WatchChanges(stream)` in v2.
- Resolved: —

### controller-redeployment-detection
- Opened: 2026-04-25
- Severity: low
- Area: `cmd/livepeer-service-registry-daemon`
- Description: Contract address resolution happens once at startup. If Livepeer redeploys `ServiceRegistry`, we keep calling the stale address until restart. Acceptable for v1 (redeployments are rare + announced), but a periodic re-resolution would be more robust.
- Remediation: hourly re-resolve via Controller; log warn if the resolved address changed.
- Resolved: —

### docker-image-publishing
- Opened: 2026-04-25
- Severity: low
- Area: build / release
- Description: `Dockerfile` + `make docker-build` produce a local image but nothing publishes tagged images.
- Remediation: GHCR publishing workflow on tagged releases.
- Partial: 2026-04-25 — `make docker-push DOCKER_TAG=vX.Y.Z` now pushes both the version tag and `:latest` to `tztcloud/livepeer-service-registry-daemon` on Docker Hub. `compose.yaml` consumes the published image. First published version was `v0.8.10`.
- Resolved: 2026-04-26 — plan 0008 landed `.github/workflows/docker.yml`, which builds + pushes all three module images (payment, service-registry, protocol) on tag push. Stable tags also re-tag `:latest`; pre-release tags push only the version tag. First fully-CI-published release is `v1.1.0`.

### docker-multi-arch
- Opened: 2026-04-25
- Severity: low
- Area: build / release
- Description: `Dockerfile` builds amd64 only. The CI publishing workflow (`.github/workflows/docker.yml`, landed via plan 0008) currently sets no `platforms:` argument on `docker/build-push-action`, so the published image is whatever the GitHub runner's arch is (amd64). Operators on arm64 hosts (Graviton, Apple Silicon servers) can't consume the image directly.
- Remediation: add `platforms: linux/amd64,linux/arm64` to the `docker/build-push-action@v7` step in `docker.yml`. Both base images (`golang:1.25-alpine`, `gcr.io/distroless/static:nonroot`) already have arm64 variants.
- Resolved: —

### grpc-tls-listener
- Opened: 2026-04-25
- Severity: low
- Area: `internal/runtime/grpc`
- Description: Daemon binds unix-socket only. Operators running across hosts (rare for resolver, never for publisher) currently need a sidecar TLS proxy.
- Remediation: optional `--listen-tcp=host:port` with required `--tls-cert/--tls-key/--client-ca`. Backlog; v1 unix-socket-only is the security posture.
- Resolved: —

### audit-log-retention-tuning
- Opened: 2026-04-25
- Severity: low
- Area: `internal/repo/audit`
- Description: Audit-log retention is a hardcoded 30 days. Some operators want 7d (tight disk budgets), some want 1y (compliance).
- Remediation: `--audit-retention=DURATION` flag with sensible bounds.
- Resolved: —

### overlay-hot-reload-tests
- Opened: 2026-04-25
- Severity: low
- Area: `internal/config`
- Description: SIGHUP reload is implemented, but `fsnotify`-based watch is brittle on certain network filesystems. Tests cover the happy path; we don't have a stress test for rapid edits.
- Remediation: add a `go test -race` stress test that races writes + reloads; document NFS caveat in operations.
- Resolved: —

### prometheus-rules-ci-check
- Opened: 2026-04-25
- Severity: low
- Area: CI / docs
- Description: `docs/operations/prometheus/alerts.yaml` ships shippable alert rules but CI doesn't validate them with `promtool check rules`. A future PR could break a rule's PromQL or YAML structure and we wouldn't catch it until an operator deployed it.
- Remediation: add a CI step that runs `promtool check rules docs/operations/prometheus/alerts.yaml` (the prometheus/promtool docker image is the lightest-touch). Same for any future alert files under that directory.
- Resolved: —

### resolver-event-driven-discovery
- Opened: 2026-04-26
- Severity: low
- Area: `internal/service/resolver`
- Description: Plan 0009 §C ships pool-walk discovery anchored to round transitions — correct but coarse, since the resolver re-walks the entire pool once per round (~19 hours on Arbitrum One) even if nothing changed. A finer model: subscribe to `BondingManager` `Bond` / `Unbond` / `Rebond` events plus the `ServiceRegistry` URI-update event and incrementally update the cache. Cache then converges in seconds when an orch joins/leaves instead of waiting for the next round boundary.
- Remediation: build the event-watching path on top of `chain-commons.providers.receipts.reorg` (reorg-aware) and gate it behind a flag so operators can fall back to pool-walk if it misbehaves. v2 concern.
- Resolved: —

### publisher-http-probe-impl
- Opened: 2026-04-27
- Severity: low
- Area: `internal/service/publisher` + proto `Publisher.ProbeWorker`
- Description: The `ProbeWorker(url)` gRPC method is still a stub
  (`Unimplemented`). Plan 0009 §D shipped the operator-facing
  `BuildAndSign` + `livepeer-registry-refresh` CLI assuming
  fully-formed Nodes in the worker-list file (operator hand-curates).
  Wiring an actual HTTP probe — fetching the worker URL, parsing a
  JSON Node response, and merging with operator-supplied metadata —
  is deferred until the worker-side capabilities convention is
  settled (the schema each workload binary should expose).
- Remediation: define the worker-side HTTP convention (default route
  like `/.well-known/livepeer-capabilities` returning `Node` JSON);
  implement HTTP fetch with `--worker-probe-timeout`; extend
  `BuildAndSign` to optionally probe entries that supply only a URL.
  Tracked for the follow-up plan once worker conventions stabilize.
- Resolved: —
