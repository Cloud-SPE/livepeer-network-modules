# service-registry-daemon

A service-registry daemon component that decouples orchestrator/worker capability discovery from `go-livepeer`. Any application — gateways, bridges, AI routers, transcoding clients — uses Livepeer's discovery rails by talking to this daemon over a local unix-socket gRPC surface. No more depending on `go-livepeer`'s monolithic discovery code paths.

> **Agent-first repository.** If you're making changes, start with [AGENTS.md](AGENTS.md) → [DESIGN.md](DESIGN.md) → [docs/design-docs/](docs/design-docs/). Everything significant is gated on an exec-plan in [docs/exec-plans/](docs/exec-plans/).

## What it does

One binary, two modes:

| Mode | Role | Exposes | Uses |
|---|---|---|---|
| `--mode=publisher` | Orchestrator-side (operator that wants their capabilities discoverable) | `Publisher` gRPC — `BuildManifest`, `SignManifest`, reserved `ProbeWorker` stub | Local keystore |
| `--mode=resolver`  | Consumer-side (gateway / bridge / AI router that wants to find orchestrators) | `Resolver` gRPC — `ResolveByAddress`, `Select`, `ListKnown`, `Refresh`, `GetAuditLog` | Chain RPC (read-only), HTTP for manifest fetch, BoltDB cache, optional static `nodes.yaml` overlay |

Under the hood the daemon implements a **single-registry, signed-manifest discovery flow**:

1. The gateway-side resolver is configured with exactly one registry contract address via `--service-registry-address`.
2. The on-chain `getServiceURI(orch)` value is treated as the full manifest URL to fetch.
3. The resolver fetches that exact URL, verifies the signature against the eth address from the chain, and returns structured orch metadata plus a gateway-facing selected-route API for pricing and dispatch.
4. If an operator shipped the rejected on-chain CSV format, the resolver decodes it read-only as a compatibility mode.
5. If the chain has no entry for an address but the operator overlay carries pin nodes for it, the resolver serves those pins directly — `--discovery=overlay-only` deployments work without any on-chain registration.

This is **workload-agnostic**. The daemon does not know what "transcoding", "AI", or "openai" mean. Capabilities are opaque strings; consumers interpret them. The same binary serves AI inference, video transcoding, and capability types invented next year — adding a new workload type requires zero code changes in this repo.

## Why this exists

`go-livepeer`'s discovery flow is monolithic and tightly coupled to its gRPC `OrchestratorInfo` schema. Existing AI integrations (e.g. the `openai-livepeer-bridge` / `openai-worker-node` pair) prove capability discovery can live entirely off-chain over plain HTTP. What was missing:

1. **A standard format** so any consumer can find any orchestrator's capabilities without knowing the operator out-of-band.
2. **Signed claims** so a resolver can trust capability metadata that wasn't put on chain.
3. **A legacy-compat story** so old transcoding clients keep working while new clients get richer data.
4. **Decoupling** — registry concerns shouldn't live in a 100k+ LoC media-processing binary.

A 2026-Q1 proposal suggested encoding richer metadata on chain by overloading `serviceURI` as a CSV (`<url>,<v>,<base64-json>`). It was reviewed and rejected — see [`docs/references/csv-proposal-review.md`](docs/references/csv-proposal-review.md). On-chain stays a single URL pointer; everything structured lives in an off-chain manifest at that exact URL.

## Quick start

### Dev mode (no chain, fake providers)

```sh
make build
./bin/livepeer-service-registry-daemon --mode=resolver --dev --socket=/tmp/registry.sock
```

Dev mode uses in-memory fakes for Chain / Signer / Store and generates a throwaway signing key at each boot in publisher mode. Useful for local development and CI. A loud `=== DEV MODE ===` banner prints to stderr.

### Production mode — resolver (Arbitrum One)

Resolver deployments should pass the exact registry contract they intend to use for `getServiceURI()` lookups; chain-id (42161) is sanity-checked at boot.

```sh
./bin/livepeer-service-registry-daemon \
  --mode=resolver \
  --socket=/var/run/livepeer-service-registry.sock \
  --store-path=/var/lib/livepeer/registry-cache.db \
  --chain-rpc=https://arb1.arbitrum.io/rpc \
  --service-registry-address=0xC92d06C74A26B312bcDE600F0aA22EAC2efA0a90 \
  --static-overlay=/etc/livepeer/nodes.yaml
```

### Production mode — publisher

Publisher needs a V3 keystore (the orchestrator's eth identity):

```sh
export LIVEPEER_KEYSTORE_PASSWORD="$(cat /etc/livepeer/ks-password)"
./bin/livepeer-service-registry-daemon \
  --mode=publisher \
  --socket=/var/run/livepeer-service-registry-publisher.sock \
  --chain-rpc=https://arb1.arbitrum.io/rpc \
  --keystore-path=/etc/livepeer/keystore.json \
  --manifest-out=/var/www/livepeer/.well-known/livepeer-registry.json
```

The publisher writes the signed manifest JSON to `--manifest-out` whenever `SignManifest` is invoked over gRPC; the operator's existing HTTP server (Caddy / nginx / etc.) serves it at the on-chain `serviceURI`. The publisher does NOT compete with the operator's TLS / port story.

The current v3.0.1 binary is signing-only in publisher mode. On-chain
`ServiceRegistry.setServiceURI` submission now belongs to
`protocol-daemon`; this daemon only builds and signs manifests.
`ProbeWorker` remains reserved and returns failed-precondition today.

See [`docs/operations/running-the-daemon.md`](docs/operations/running-the-daemon.md) for the full flag reference.

### Docker

The image is published at [`tztcloud/livepeer-service-registry-daemon`](https://hub.docker.com/r/tztcloud/livepeer-service-registry-daemon) on Docker Hub:

```sh
docker pull tztcloud/livepeer-service-registry-daemon:v3.0.1
```

For a turn-key resolver, copy the run-only example env and bring up the stack:

```sh
cp compose/.env.example .env     # set CHAIN_RPC; pin TAG
docker compose -f compose/docker-compose.yml up -d
docker compose logs -f
```

The image is ~21 MB (distroless/static base, pure-Go build, runs as nonroot uid 65532). Tags published per release; pin to a specific version (e.g. `v3.0.1`) rather than `latest` for production. Multi-arch images (amd64 + arm64) are tracked in [tech-debt](docs/exec-plans/tech-debt-tracker.md) under `docker-multi-arch`.

### See it end-to-end

```sh
go run ./examples/minimal-e2e/...
```

A single Go program that builds + signs a manifest with two AI nodes and one transcoding node, hands it to a resolver pointed at an in-memory chain, and shows `Resolve` plus gateway-facing `Select` route selection. Output:

```
Operator address: 0x3ecb9b37a1fded7ce5e21dd90ca8a81879491016
Signed manifest: 1071 bytes, sig=0x40992373db2c...
Resolve: mode=well-known nodes=3
  - ai-east @ https://ai-east.example.com:8935 sig=signed-verified caps=[openai:/v1/chat/completions openai:/v1/embeddings]
  - ai-west @ https://ai-west.example.com:8935 sig=signed-verified caps=[openai:/v1/chat/completions]
  - transcoder-1 @ https://orch.example.com:8935 sig=signed-verified caps=[livepeer:transcoder/h264]
Select(transcoder/h264, h264-main): worker=https://orch.example.com:8935 recipient=0x3ecb9b37a1fded7ce5e21dd90ca8a81879491016 price=2000/frame
Select(chat/completions, gpt-oss-20b): worker=https://ai-east.example.com:8935 recipient=0x3ecb9b37a1fded7ce5e21dd90ca8a81879491016 price=1000/token
```

A second example in `examples/static-overlay-only/` boots a resolver with no chain at all — purely operator-curated `nodes.yaml`. The daemon walks the overlay at startup and pre-resolves each entry into the cache (chainless `static-overlay` synth mode), so `ListKnown` / `Select` reflect the pool without a manual `Refresh`. Useful for bootstrap and bridge migration scenarios.

## Architecture at a glance

```
                                Static overlay (nodes.yaml)
                                       │
  ┌─────────┐    GetServiceURI    ┌────▼────┐    /.well-known    ┌──────────────┐
  │  Chain  │◄────────────────────│Resolver │────────────────────►Manifest host │
  │(on-chain│                     │ daemon  │   verify signature │ (operator's  │
  │ pointer)│                     │         │                    │  HTTP server)│
  └─────────┘                     └────┬────┘                    └──────────────┘
                                       │ unix-socket gRPC
                                       ▼
                                ┌──────────────┐
                                │ Consumer app │
                                │ (bridge,     │
                                │  gateway,    │
                                │  router)     │
                                └──────────────┘
```

The publisher side mirrors this in reverse: build → sign → write JSON
to disk. In v3.0.1 the one-time or follow-on `setServiceURI` write is
owned by `protocol-daemon`, not this repo.

The internal layout follows a strict layer rule: `types → config → repo → service → runtime`, with all I/O (chain RPC, HTTP, BoltDB, keystore) entering through `internal/providers/`. golangci-lint's `depguard` enforces the boundary mechanically.

For the full mental model — **component map, publish/resolve sequence diagrams, trust model, and per-RPC reference** — see [`docs/design-docs/architecture.md#information-flow`](docs/design-docs/architecture.md#information-flow). Diagrams render as Mermaid on GitHub.

## Highlights

- **Explicit single-registry resolution** — each resolver deployment reads exactly one configured registry contract address and treats the on-chain `serviceURI` value as the manifest URL to fetch. See [`docs/design-docs/serviceuri-modes.md`](docs/design-docs/serviceuri-modes.md).
- **Signed claims, recovered against chain identity** — every manifest is signed `eth-personal-sign` over canonical bytes. The resolver recovers the signer and verifies it matches the eth address whose `serviceURI` pointed us there. Mismatch is rejected with `signature_mismatch` and never cached. See [`docs/design-docs/signature-scheme.md`](docs/design-docs/signature-scheme.md).
- **Workload-agnostic capability namespace** — `openai:/v1/chat/completions`, `livepeer:transcoder/h264`, `livepeer-byoc:my-thing` are all opaque to the daemon. No code change needed to ship a new capability type. See [`docs/design-docs/workload-agnostic-strings.md`](docs/design-docs/workload-agnostic-strings.md).
- **Static overlay as policy authority** — the on-chain manifest is canonical for what the operator advertises; operator-curated `nodes.yaml` is canonical for what the consumer accepts (`enabled`, `tier_allowed`, `weight`, `unsigned_allowed`). Augment, don't replace. SIGHUP / fsnotify hot-reload. See [`docs/design-docs/static-overlay.md`](docs/design-docs/static-overlay.md).
- **Last-good fallback with audit trail** — refresh failures don't evict the cache. Resolver returns the last-good entry with `freshness_status: stale_failing` so consumers can apply their own circuit-breaker policy. Every cache transition is logged to a queryable audit bucket. See [`docs/design-docs/resolver-cache.md`](docs/design-docs/resolver-cache.md).
- **Boundary-validating decoder** — JSON enters the system through exactly one function (`types.DecodeManifest`), which enforces schema, eth-address case, URL scheme, signature shape, duplicate-id checks. A custom lint (`lint/no-unverified-manifest`) flags any other code that tries to `json.Unmarshal` into a `*types.Manifest`.
- **Structured logging** via stdlib `log/slog` with `--log-level={debug,info,warn,error}` and `--log-format={text,json}`. Every chain read, manifest fetch, signature recovery, cache transition, and publisher-mode reserved-stub failure is a structured event with a stable error-code string.
- **Verbose Prometheus metrics** opt-in via `--metrics-listen=:9091`. Resolutions broken down by mode × freshness, signature-mismatch rates, cache hit ratio, chain RPC latency p99, in-flight gRPC counts — every layer is dashboard-legible. Cardinality-capped to keep Prometheus memory bounded under accidental label-explosion. See [`docs/design-docs/observability.md`](docs/design-docs/observability.md). A 30-panel **Grafana dashboard** ships at [`docs/operations/grafana/`](docs/operations/grafana/), and a 12-rule **Prometheus alert** file at [`docs/operations/prometheus/`](docs/operations/prometheus/) — both import-and-go.
- **Preflight at startup** — keystore decrypt, store open, signer→address derivation. Misconfiguration fails loud before the gRPC socket ever opens.
- **One binary, two modes** — publisher and resolver share the same code, the same lints, the same release. Mode-specific RPCs are gated at startup; calling a publisher RPC on a resolver daemon returns `Unimplemented`, never a confusing error.
- **Resolver-side contract is explicit** — gateway deployments choose one registry contract in config instead of relying on Controller-derived defaults or multi-target probing.

## Status

The initial scaffold (exec-plan [`0001-repo-scaffold`](docs/exec-plans/completed/0001-repo-scaffold.md)) landed everything you see here: the layered Go code, the manifest pipeline, providers (chain/signer/verifier/fetcher/store/clock/logger), repo (cache + audit), services (resolver/publisher/selection/legacy), Go-native runtime, CLI, two examples, custom lints, CI workflows, Dockerfile, full docs.

[`0002-grpc-wire-binding`](docs/exec-plans/completed/0002-grpc-wire-binding.md) followed: generated proto stubs under `proto/gen/` are bound to a real `*grpc.Server` listening on a unix socket via `internal/runtime/grpc/listener.go`, with panic-recovery + per-RPC-deadline + structured-logging interceptors, gRPC standard health, and reflection. The Go-native handler surface in `internal/runtime/grpc/server.go` remains as the in-process integration target the adapters delegate to.

Tech debt is tracked in [`docs/exec-plans/tech-debt-tracker.md`](docs/exec-plans/tech-debt-tracker.md). Notable backlog items: HSM/KMS signer, hot/cold delegation, manifest-update streaming RPC, Controller-resolved contract addresses with periodic re-resolve, multi-arch Docker images.

## Repo layout

```
.
├── AGENTS.md                       # repo map + invariants (≤ 100 lines, table of contents)
├── DESIGN.md                       # full architecture
├── PLANS.md                        # how exec-plans work
├── PRODUCT_SENSE.md                # who this is for / what "good" looks like
├── README.md                       # this file
├── LICENSE                         # MIT
├── Makefile                        # build / test / lint / proto / docker
├── Dockerfile                      # distroless/static, ~13 MB
├── compose.yaml                    # example resolver-mode deployment
├── registry.example.yaml           # example operator overlay
│
├── cmd/
│   └── livepeer-service-registry-daemon/  # main binary
│
├── proto/
│   ├── buf.yaml / buf.gen.yaml     # buf v2 config
│   ├── livepeer/registry/v1/       # proto source-of-truth (types, resolver, publisher)
│   └── gen/go/livepeer/registry/v1/  # generated bindings (committed)
│
├── internal/
│   ├── types/                      # pure data: Manifest, Node, EthAddress, errors, canonical bytes
│   ├── config/                     # validated daemon config + overlay parser
│   ├── providers/                  # cross-cutting I/O interfaces
│   │   ├── chain/                  # ServiceRegistry RPC (in-memory + go-ethereum eth_call)
│   │   ├── signer/                 # V3 keystore + eth-personal-sign
│   │   ├── verifier/               # secp256k1 recover
│   │   ├── manifestfetcher/        # HTTPS GET with size + timeout caps
│   │   ├── store/                  # BoltDB + in-memory
│   │   ├── clock/                  # System + Fixed (test)
│   │   └── logger/                 # slog wrapper
│   ├── repo/
│   │   ├── manifestcache/          # cached resolve entries
│   │   └── audit/                  # event log for operator visibility
│   ├── service/
│   │   ├── resolver/               # the main flow (chain → mode-detect → fetch → verify → overlay → cache)
│   │   ├── publisher/              # build → sign (+ reserved probe stub)
│   │   ├── selection/              # filter + rank by capability/model/tier/geo/weight
│   │   └── legacy/                 # synthesize a single legacy node from a plain-URL serviceURI
│   ├── runtime/
│   │   ├── grpc/                   # Go-native handler surface (gRPC adapter lands in 0002)
│   │   └── lifecycle/              # daemon boot/shutdown, signal handling
│   └── utils/                      # zero-dep helpers
│
├── lint/
│   ├── doc-gardener/               # frontmatter + cross-link checker
│   ├── no-unverified-manifest/     # boundary-decoder enforcer
│   ├── coverage-gate/              # stub (per-package threshold; queued in tech-debt)
│   └── layer-check/                # stub (depguard covers v1; richer go vet analyzer queued)
│
├── examples/
│   ├── minimal-e2e/                # full pipeline in one Go program
│   └── static-overlay-only/        # chain-free YAML-only resolver run
│
├── docs/
│   ├── design-docs/                # 8 design docs + index + core-beliefs
│   ├── product-specs/              # 3 stable contract docs
│   ├── operations/                 # operator runbook
│   ├── references/                 # external material (CSV proposal review, capability enum mapping)
│   ├── exec-plans/                 # active/completed plans + tech-debt tracker
│   └── generated/                  # auto-generated artifacts (manifest example)
│
└── .github/workflows/              # CI: lint + test
```

## Build & test

| Command | What it does |
|---|---|
| `make build` | Build `bin/livepeer-service-registry-daemon` |
| `make test` | `go test -race ./...` — full unit + table tests |
| `make lint` | `golangci-lint run` + custom Go lints (`doc-gardener`, `no-unverified-manifest`) |
| `make proto` | `buf lint && buf generate` — regenerate `proto/gen/go/...` |
| `make doc-lint` | Just the doc-gardener (frontmatter + cross-link check) |
| `make tidy` | `go mod tidy` |
| `make docker-build` | Build a tagged image (`DOCKER_TAG=...`) |
| `make clean` | Remove `bin/` |

Quality gates per [`docs/design-docs/core-beliefs.md`](docs/design-docs/core-beliefs.md): `≥ 75%` per-package statement coverage (gate stub today, full enforcement in [tech-debt](docs/exec-plans/tech-debt-tracker.md) `coverage-gate-impl`); `depguard` enforces the layer rule mechanically; `doc-gardener` fails CI on stale frontmatter or broken cross-links.

## Compatibility matrix

| Caller | Their behavior | This daemon's behavior | Result |
|---|---|---|---|
| go-livepeer transcoding client (legacy) | reads `serviceURI`, dials it | publisher writes a plain URL there | works (legacy mode) |
| New consumer using this daemon's resolver | calls `Resolver.ResolveByAddress` | resolver finds well-known manifest → returns rich nodes | works (well-known mode) |
| Same consumer, against an orchestrator that ships only a URL | same call | resolver returns single legacy-synthesized node | works (legacy fallback) |
| Same consumer, against a CSV-format `serviceURI` | same call | resolver decodes CSV read-only, marks nodes unsigned | works (CSV mode, opt-in) |
| Same consumer, against an unregistered address with an operator overlay entry that has pins | same call (overlay-only daemon) | resolver serves the overlay's pins | works (static-overlay synth mode) |
| go-livepeer's existing `OrchestratorInfo` gRPC | unchanged | this daemon does not touch it | unchanged |

See [`docs/product-specs/legacy-compat.md`](docs/product-specs/legacy-compat.md) for the full guarantees.

## Where to read next

- [AGENTS.md](AGENTS.md) — repo map, layer rule, invariants
- [DESIGN.md](DESIGN.md) — full architecture
- [PLANS.md](PLANS.md) — how work is planned
- [PRODUCT_SENSE.md](PRODUCT_SENSE.md) — who this is for, what "good" looks like
- [docs/design-docs/architecture.md#information-flow](docs/design-docs/architecture.md#information-flow) — Mermaid component map + publish / resolve / trust diagrams
- [docs/design-docs/manifest-schema.md](docs/design-docs/manifest-schema.md) — manifest format
- [docs/design-docs/serviceuri-modes.md](docs/design-docs/serviceuri-modes.md) — four resolver modes (three legacy-compat + chainless static-overlay synth)
- [docs/product-specs/grpc-surface.md](docs/product-specs/grpc-surface.md) — gRPC API
- [docs/design-docs/observability.md](docs/design-docs/observability.md) — Prometheus metrics catalog + sample queries
- [docs/operations/grafana/](docs/operations/grafana/) — Grafana dashboard JSON + import guide
- [docs/operations/prometheus/](docs/operations/prometheus/) — Prometheus alert rules YAML (page / ticket / info tiers)
- [docs/operations/running-the-daemon.md](docs/operations/running-the-daemon.md) — operator guide
- [docs/references/csv-proposal-review.md](docs/references/csv-proposal-review.md) — why on-chain CSV was rejected

## License

MIT — see [LICENSE](LICENSE). Same license as [go-livepeer](https://github.com/livepeer/go-livepeer) so consumers can mix and match without a license-compatibility audit.
