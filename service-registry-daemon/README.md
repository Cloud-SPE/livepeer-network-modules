# service-registry-daemon

Consumer-side discovery and route selection over local unix-socket gRPC.
Capabilities are opaque strings; the daemon does not execute workloads or
process payments. Start maintenance work at [AGENTS.md](AGENTS.md).

## What it does

| Mode | Public surface | Dependencies |
|---|---|---|
| `resolver` | `ResolveByAddress`, `Select`, `SelectMany`, `ListKnown`, `Refresh`, `GetAuditLog`, `Health` | Manifest HTTP fetcher, signature verifier, cache and audit store; chain providers only for chain discovery |
| `publisher` | `GetIdentity`, `Health` | Local keystore and store; no manifest building, signing, hosting or chain writes |

The coordinator builds a protocol manifest from broker offerings. The operator
signs it on the cold host using `secure-orch-console`, then uploads it to the
coordinator, which serves `/.well-known/livepeer-registry.json`. The registry
fetches and verifies that signed publication and returns broker routes, prices,
protocol declarations and settlement keys. See [architecture](docs/design-docs/architecture.md).

## Docker-first setup

Build from this component directory:

```sh
make docker-build DOCKER_TAG=dev
```

The build context includes the sibling `chain-commons` and `proto-contracts`
modules. The runtime image is distroless, runs as UID 65532, and writes only to
its socket and state directories. This command builds locally; it does not
publish an image. Published image tags follow the monorepo release.

For the resolver Compose deployment:

```sh
cp compose/.env.example compose/.env
# Edit compose/.env for the discovery source and image tag.
docker compose --env-file compose/.env -f compose/docker-compose.yml config
docker compose --env-file compose/.env -f compose/docker-compose.yml up -d
```

The checked-in Compose image default is `v2.0.0`; set `TAG=dev` to use the local
build, or select the published release containing the needed changes. A tag
name alone is not evidence that a running container includes a particular commit.

### Discover coordinators from YAML

```yaml
overlay:
  - eth_address: "0x0123456789abcdef0123456789abcdef01234567"
    manifest_url: "https://coordinator.example.com/.well-known/livepeer-registry.json"
```

`manifest_url` locates the **coordinator**. The signed manifest supplies the
**broker** URLs to which gateways submit work. The address in YAML is the
expected orchestrator signer. Do not point `manifest_url` at the broker's
unsigned `/registry/offerings` response.

For Compose, set these in `compose/.env`:

```dotenv
DISCOVERY_MODE=overlay-only
STATIC_OVERLAY=/etc/livepeer/nodes.yaml
STATIC_OVERLAY_HOST_PATH=/absolute/path/to/nodes.yaml
CHAIN_RPC_URLS=
```

The equivalent daemon flags are:

```sh
--mode=resolver --discovery=overlay-only --static-overlay=/path/to/nodes.yaml
```

This production mode bypasses chain enumeration and `serviceURI` lookup. It
needs neither registry chain RPC nor `--dev`. Payment components retain their
normal chain configuration. The older `pin.url` injects an unsigned static
route; it is not a manifest pointer.

### Discover through the chain

Set `DISCOVERY_MODE=chain` and supply `CHAIN_RPC_URLS`. The resolver reads
`serviceURI` from the AI registry address, which is set by default. An empty
`--ai-service-registry-address` selects the primary `ServiceRegistry` instead
(Controller-derived unless overridden); there is no fallback between the two.
Active orchestrators are seeded on round events. Explicit overlay manifest pointers still take
precedence for their addresses. See [discovery modes](docs/design-docs/serviceuri-modes.md).

### Identity-only publisher

The root [compose.yaml](compose.yaml) is an identity-only publisher example.
It loads a V3 keystore and answers `GetIdentity` and `Health`; it cannot sign or
publish a manifest. Normal publication belongs to the coordinator and cold
console. Do not move the cold orchestrator key to a gateway or broker host.

## Consumer behavior

`Select` returns the first ranked route; `SelectMany` returns the ordered set.
Both require a capability and offering, apply tier/weight policy, and consult
broker live health. Route discovery does not authorize work or validate ticket
funding; those checks belong to the broker and payment components.

Manifest TTL refresh is synchronous on demand. A forced `Refresh` bypasses TTL.
An unavailable source can use bounded last-good data; invalid publications do
not use that manifest-outage fallback. Restart to reload edited YAML.

Read the [gRPC contract](docs/product-specs/grpc-surface.md),
[cache behavior](docs/design-docs/resolver-cache.md), and
[manifest validation limits](docs/product-specs/manifest-contract.md) before
integrating. In particular, diagnostic `Health` is not a readiness guarantee.

## Development and verification

The host-development targets require the Go version in [go.mod](go.mod):

```sh
make build
make test
make lint
make docs-generate
```

`make test` uses the race detector. Go lint enforces dependency boundaries;
[custom lints](lint/README.md) check documentation and suspicious manifest
decoding. `make coverage-check` enforces 75% statement coverage for each executable
cmd/internal package; `make ship-check` includes that gate.

The [minimal example](examples/minimal-e2e/README.md) signs a test manifest
in-process with a throwaway key and selects routes. The
[static pin example](examples/static-overlay-only/README.md) demonstrates
unsigned configuration. Neither sends payments or redeems tickets.

## Documentation map

- [DESIGN.md](DESIGN.md): component ownership and source map.
- [Running the daemon](docs/operations/running-the-daemon.md): flags, startup, shutdown and Compose.
- [Static overlay](docs/design-docs/static-overlay.md): coordinator discovery versus static pins.
- [Design index](docs/design-docs/index.md): detailed contracts and implementation limits.
- [Metrics](docs/design-docs/observability.md), [Grafana](docs/operations/grafana/README.md), [alerts](docs/operations/prometheus/README.md).
- [Protocol manifest](../livepeer-network-protocol/manifest/README.md): authoritative signed format.
- [Historical debt notes](docs/exec-plans/tech-debt-tracker.md): dated observations; beads is the current work tracker.
