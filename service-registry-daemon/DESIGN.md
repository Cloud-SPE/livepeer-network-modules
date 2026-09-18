# DESIGN — service-registry-daemon

The registry translates orchestrator identity into broker routes. Consumers
use local unix-socket gRPC; discovery can use chain pointers or a configured
list of coordinator manifest URLs.

## Ownership

| Component | Responsibility |
|---|---|
| `capability-broker` | Offers inventory, live route health, workload admission and dispatch |
| `orch-coordinator` | Scrape broker inventory, compose candidates, receive and host signed manifests |
| `secure-orch-console` | Operator-controlled signing with the cold orchestrator key |
| `protocol-daemon` | On-chain pointer writes |
| Registry resolver | Fetch, verify, cache, project, filter and rank routes |
| Registry publisher | Load a keystore identity; expose identity and diagnostic health only |
| Gateway/payment components | Execute the chosen protocol, fund accounts, validate and redeem tickets |

The registry never builds production manifests, signs publications, writes
`serviceURI`, or executes paid work. The in-process example uses a throwaway
signer as a test fixture; that is not a publisher RPC.

## Source map and layers

`types → config → repo → service → runtime` is the dependency stack.
Cross-cutting I/O enters through `internal/providers/`. The enforced boundary
is golangci-lint `depguard`; `lint/layer-check` is a placeholder.

| Source | Behavior |
|---|---|
| `internal/types/coordinator_envelope.go` | Strict envelope decode, structural validation and node projection |
| `internal/config/overlay.go` | YAML parsing, policy and coordinator pointers |
| `internal/service/resolver` | Source selection, HTTP fetch/verify, cache and live health |
| `internal/service/selection` | Conjunctive matching and stable descending weight ordering |
| `internal/service/legacy` | Synthesize an unverified legacy endpoint |
| `internal/service/publisher` | Loaded-keystore identity |
| `internal/repo/manifestcache`, `internal/repo/audit` | Gob-encoded key/value records |
| `internal/runtime/grpc` | Wire adapters, route fingerprints, listeners and status mapping |
| `internal/runtime/seeder` | Chain pool refresh on round events |
| `internal/runtime/lifecycle` | Listener/seeder lifetime and signal handling |
| `cmd/livepeer-service-registry-daemon` | Flag parsing, provider construction and startup seeding |

Production store/clock/signer adapters use `chain-commons`; resolver chain
providers share the RPC client with Controller resolution and round polling.
Overlay-only builds no chain client. Dev uses an in-memory chain/store and a
throwaway publisher key, but real HTTP retrieval and signature verification.

## Contracts

- [Architecture and information flow](docs/design-docs/architecture.md#information-flow)
- [Discovery modes](docs/design-docs/serviceuri-modes.md)
- [Overlay](docs/design-docs/static-overlay.md)
- [Cache](docs/design-docs/resolver-cache.md)
- [gRPC consumer contract](docs/product-specs/grpc-surface.md)
- [Manifest contract and implementation limits](docs/product-specs/manifest-contract.md)

The protocol envelope is the only signed wire format. The nested
`Node → Capability → Offering` shape is the resolver's projection, not another
publishable manifest. Broker endpoints are grouped by URL and assigned
synthetic node IDs. Workload protocol declarations and opaque metadata reach
consumers without the registry interpreting workload semantics.
