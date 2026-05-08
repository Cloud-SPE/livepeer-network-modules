# DESIGN — livepeer-service-registry

## What this is

A standalone service-registry daemon that decouples orchestrator/worker discovery from `go-livepeer`. Two modes in one binary:

- **Publisher mode** (`--mode=publisher`) — builds a signed capability manifest from operator-supplied node specs, writes it to disk for the operator's HTTP server to host at a well-known path, and exposes a reserved gRPC probe stub only.
- **Resolver mode** (`--mode=resolver`) — given an Ethereum orchestrator address, returns orch-oriented node metadata via `ResolveByAddress` and a gateway-facing selected-route via `Select`. Handles legacy `serviceURI` values transparently.

Apps talk to the daemon over a local unix-socket gRPC. Apps never need to dial Ethereum, parse `serviceURI`, or know anything about the manifest format.

This is workload-agnostic. A capability is an opaque string. The same daemon serves transcoding (`livepeer:transcoder/h264`), AI inference (`openai:/v1/chat/completions`), and anything not yet imagined.

## Why it exists

`go-livepeer`'s discovery flow is monolithic and tightly coupled to its gRPC `OrchestratorInfo` schema. The bridge/worker pair already proves capability discovery can live entirely off-chain over plain HTTP. What's missing:

1. **A standard format** so any consumer can find any orchestrator's capabilities without knowing the operator out-of-band.
2. **Signed claims** so a resolver can trust capability metadata that wasn't put on-chain.
3. **A legacy-compat story** so old transcoding clients keep working while new clients get richer data.
4. **Decoupling** — registry concerns shouldn't live in a 100k+ LoC media-processing binary.

The on-chain CSV-with-base64-JSON approach was reviewed and rejected (see [docs/references/csv-proposal-review.md](docs/references/csv-proposal-review.md)). On-chain stays a single URL pointer; everything structured lives in an off-chain manifest the URL points to.

## Layer stack

```
┌─────────────────────────────────────────────────────────┐
│  runtime/           gRPC server, lifecycle, signal loop │  ← may import anything below
├─────────────────────────────────────────────────────────┤
│  service/           business logic                      │  ← may import repo, providers, config, types
│    ├─ resolver/                                         │
│    ├─ publisher/                                        │
│    ├─ selection/                                        │
│    └─ legacy/                                           │
├─────────────────────────────────────────────────────────┤
│  repo/              persistence + cache adapters        │  ← may import providers, config, types
├─────────────────────────────────────────────────────────┤
│  config/            validated structs                   │  ← may import types
├─────────────────────────────────────────────────────────┤
│  types/             pure data                           │  ← imports nothing in internal/
└─────────────────────────────────────────────────────────┘

  providers/          cross-cutting interfaces + defaults
  utils/              shared zero-dependency helpers
```

Dependency rule: a package at layer N may import only packages at layers < N, plus `providers/` and `utils/`. Enforced by `lint/layer-check` (and golangci-lint `depguard`). Full detail in [docs/design-docs/architecture.md](docs/design-docs/architecture.md).

For the end-to-end information flow — component map, publish / resolve / job-execution sequences, and the trust model — see [docs/design-docs/architecture.md#information-flow](docs/design-docs/architecture.md#information-flow).

## Domains

| Path | Purpose |
|---|---|
| `internal/service/resolver` | Resolve `ethAddress → []Node`. Reads on-chain `serviceURI`, decides mode (legacy / well-known manifest / CSV-fallback / static-overlay-synth on chain `not_found`), fetches and verifies, merges static overlay. |
| `internal/service/publisher` | Build manifest from operator-supplied node specs, sign it with the operator's eth key, and expose the reserved probe stub. |
| `internal/service/selection` | Filter + rank resolved nodes by capability/offering/tier/geo/weight. Used by both publisher (sanity) and resolver (`Select` RPC). |
| `internal/service/legacy` | The "synthesize a single legacy node from a plain URL" rule. Isolated so the legacy path is auditable on its own. |
| `internal/runtime/grpc` | Wire surface: `Publisher` and `Resolver` services from `livepeer.registry.v1`. |
| `internal/runtime/lifecycle` | Daemon boot/shutdown, mode gating, refresh schedulers. |

## Providers

All cross-cutting concerns enter through `internal/providers/`. One interface per concern; one or more implementations.

| Provider | Interface role | Default impl |
|---|---|---|
| `Chain` | Read `ServiceRegistry.getServiceURI(addr)` | `providers/chain/eth` (go-ethereum; reads only) |
| `ManifestFetcher` | HTTP GET of the exact manifest URL returned on-chain, with size/timeout caps | `providers/manifestfetcher/http` |
| `Signer` | Sign canonical manifest bytes with the operator's keystore key | `providers/signer/chaincommonsadapter` over `chain-commons.providers.keystore.v3json` |
| `Verifier` | Recover the eth address that signed a manifest; compare to the registry-claimed address | `providers/verifier/secp256k1` |
| `Clock` | System time + cache-freshness math | `providers/clock/chaincommonsadapter` over `chain-commons.providers.clock.System()` |
| `Store` | Persistent key-value for the manifest cache and audit log | `providers/store/chaincommonsadapter` over `chain-commons.providers.store.bolt` |
| `Logger` | Structured log | `providers/logger/slog` |

Providers are wired in `cmd/livepeer-service-registry-daemon/main.go` and injected into `service/` and `repo/`.

## Mode selection

One binary, two modes. `cmd/livepeer-service-registry-daemon/main.go` reads `--mode=publisher|resolver` at startup and mounts only the relevant gRPC services. All internal packages are compiled into both; runtime guards prevent cross-mode calls.

A single deployment can run *both* on the same host with two daemons (publisher serving `/.well-known/...`, resolver feeding the gateway) — they don't share state and don't need to be aware of each other.

## What this does NOT do

- **No HTTP web server for the manifest.** The publisher writes the manifest file to disk and emits the path; the operator's existing reverse proxy / orchestrator HTTP server serves it. (We do not want to compete with the operator's TLS / cert / port story.)
- **No payment.** Pricing is *advertised* in the manifest; settlement still goes through `livepeer-payment-library`.
- **No custom routing policy beyond resolver weight ordering.** `Select` returns the top-ranked route after conjunctive filtering; richer load-balancing policy still lives in the consumer app.
- **No replacement for go-livepeer's gRPC capability advertisement.** Gateways using go-livepeer's existing `GetOrchestrator` RPC keep working unchanged. The daemon is purely additive.

## Build artifacts

- Single Go binary: `livepeer-service-registry-daemon`
- Generated proto code: `proto/gen/go/livepeer/registry/v1/`
- Optional: Docker image (distroless/static, ~20 MB)

## Backwards compatibility surface

Four resolver modes, in priority order. See [docs/design-docs/serviceuri-modes.md](docs/design-docs/serviceuri-modes.md).

1. **Well-known manifest.** The on-chain `serviceURI` is the full manifest URL. The resolver fetches that exact URL. If signed and valid, all the new metadata is available.
2. **CSV-fallback (read-only).** If `serviceURI` is a CSV with a base64 segment (the format from the rejected on-chain proposal), the resolver decodes it best-effort and produces `[]Node` for compatibility with anyone who shipped that format. The publisher in this repo never *produces* CSV.
3. **Legacy URL.** The resolver returns a single synthesized `Node{URL: serviceURI, Capabilities: nil}` so transcoding clients that just need an endpoint to dial keep working.
4. **Static-overlay synth (chainless fallback).** If the chain has no entry for the address but the operator overlay carries an enabled entry with pin nodes, the resolver serves those pin nodes directly. Used by `--discovery=overlay-only` deployments and the static-overlay-only example.

The static overlay (operator-curated `nodes.yaml`) is merged on top of all four, so a bridge operator's `enabled` / `tierAllowed` / `weight` policy is always authoritative. (For mode 4, the overlay *is* the source.)
