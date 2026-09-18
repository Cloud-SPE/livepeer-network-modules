# Running the daemon

Use the component Dockerfile/Compose for deployment. `make build` and
`make test` are host-development targets using the Go version in `go.mod`.
All commands below are run from `service-registry-daemon/` unless stated.

## Modes and discovery

- Resolver mode fetches/verifies manifests, stores cache/audit state and serves
  inventory and selected routes over a unix socket.
- Publisher mode loads a V3 keystore and serves GetIdentity/Health only. It
  does not build/sign/write/host manifests or submit chain transactions.

Production chain discovery requires `--chain-rpc-urls`. It resolves Controller
addresses, seeds active orchestrators on round events, and reads serviceURI
per address. Explicit overlay manifest pointers take precedence.

Production overlay-only uses enabled YAML entries as its entire discovery set.
`manifest_url` points to the coordinator's signed manifest, whose tuples contain
broker URLs. No registry RPC connection or dev flag is required. Static pins
are a separate unsigned route source.

```yaml
overlay:
  - eth_address: "0x0123456789abcdef0123456789abcdef01234567"
    manifest_url: "https://coordinator.example.com/.well-known/livepeer-registry.json"
```

```sh
--mode=resolver --discovery=overlay-only --static-overlay=/path/to/nodes.yaml
```

## Compose

```sh
cp compose/.env.example compose/.env
# Edit the file for the selected source and image.
docker compose --env-file compose/.env -f compose/docker-compose.yml config
docker compose --env-file compose/.env -f compose/docker-compose.yml up -d
```

For overlay-only, set `DISCOVERY_MODE=overlay-only`,
`STATIC_OVERLAY=/etc/livepeer/nodes.yaml`, and
`STATIC_OVERLAY_HOST_PATH=/absolute/path/to/nodes.yaml`. `CHAIN_RPC_URLS` may be
empty. The file must exist on the host; the Compose bind is read-only.

For chain mode, supply `CHAIN_RPC_URLS`. `STATIC_OVERLAY` defaults to empty, so
the mounted example file is not applied unless explicitly selected. The daemon
validates required chain configuration at startup. Compose defaults for TTL,
metrics port and size cap differ from binary defaults; the env example records
the Compose values.

Both Compose files keep image tag defaults unchanged. Set `TAG` for the
resolver or `REGISTRY_IMAGE_TAG` for the identity-only publisher. For local
build testing, `make docker-build DOCKER_TAG=dev` and set the corresponding tag
to `dev`. No signing or chain RPC is needed by publisher mode. Its keystore and
password bind mounts must be readable by runtime UID 65532.

## Binary flags

| Flag | Binary default | Actual behavior |
|---|---|---|
| `--mode` | required | resolver or publisher |
| `--socket` | `/var/run/livepeer-service-registry.sock` | Unix gRPC listener |
| `--store-path` | `/var/lib/livepeer/registry-cache.db` | BoltDB in production, in-memory in dev |
| `--chain-rpc-urls` | empty | Comma-separated RPC list; required for production chain discovery only |
| `--controller-address` | Arbitrum One Controller | Supplies primary registry, pool and round addresses |
| `--service-registry-address` | empty | Primary registry override; empty derives from Controller |
| `--ai-service-registry-address` | Arbitrum One AI registry | Sole serviceURI lookup contract when nonempty; empty selects primary registry |
| `--chain-id` | 42161 | Expected identity; every production chain-discovery RPC endpoint must respond with this chain ID at startup |
| `--discovery` | chain | chain enumeration or overlay-only configured list |
| `--round-poll-interval` | 1m | Round-transition polling in chain mode |
| `--cache-manifest-ttl` | 10m | Synchronous on-demand manifest refresh after this age |
| `--manifest-max-bytes` | 4194304 | Body size cap, allowed range 1024 through 16 MiB |
| `--manifest-fetch-timeout` | 5s | Per HTTP attempt, including alternate candidate paths |
| `--max-stale` | 1h | Age bound for last-good failure fallback; also internal chain freshness bound |
| `--static-overlay` | empty | YAML loaded once at startup; restart to reload |
| `--reject-unsigned` | true | Default policy for unsigned static/CSV nodes; never permits unsigned coordinator envelopes |
| `--worker-probe-timeout` | 5s | Resolver live broker health fetch timeout |
| `--keystore-path` | empty | Required by production publisher |
| `--keystore-password-file` | empty | Password file; otherwise LIVEPEER_KEYSTORE_PASSWORD environment value |
| `--orch-address` | empty | Retained no-op; does not override keystore identity |
| `--manifest-out` | empty | Retained no-op; removed signing RPCs do not write this path |
| `--dev` | false | In-memory chain/store; throwaway publisher key; real verifier and HTTP fetcher |
| `--chain-seed` | empty | Dev-only address/service_uri YAML; incompatible with overlay-only |
| `--log-format` | text | text or json |
| `--log-level` | info | debug, info, warn, error |
| `--metrics-listen` | empty | Optional TCP metrics/liveness listener |
| `--metrics-path` | /metrics | Metrics route |
| `--metrics-max-series-per-metric` | 10000 | Label cardinality cap; 0 disables |

## Dev and chain seeds

Dev does not replace the verifier or fetcher with fakes. Without a chain seed
it forces overlay-only. With `--chain-seed`, it retains chain-style source
resolution over an in-memory map and performs signed HTTP fetch/verification.
It cannot be combined with `--chain-rpc-urls`.

```yaml
seed:
  - eth_address: "0xabc0000000000000000000000000000000000000"
    service_uri: "http://127.0.0.1:9099/.well-known/livepeer-registry.json"
```

```sh
./bin/livepeer-service-registry-daemon --mode=resolver --dev   --chain-seed ./seed.yaml --socket /tmp/registry.sock
```

Serve a properly signed envelope matching the expected identity. Explicit
chain seeds are force-resolved before readiness; any failure fails startup.
The [in-process example](../../examples/minimal-e2e/README.md) creates a signed
test fixture; publisher mode cannot generate one through RPC.

## Startup, refresh and diagnostics

Overlay seeding resolves enabled entries before readiness, best-effort.
Manifest pointers fetch signed publications; static pins synthesize nodes.
There is no chain lookup in overlay-only. Failed seeds are logged and skipped;
configured pointers are retried on subsequent Select/Refresh calls. ListKnown
shows cached candidates only. A successful startup may still have zero routes.

Fresh manifest cache entries are reused; stale requests refresh synchronously.
Forced Refresh bypasses TTL. Wildcard Refresh retries candidates but suppresses
per-address errors. Use a specific address, logs and audit records to diagnose
failures. See [cache semantics](../design-docs/resolver-cache.md).

Health RPC provider booleans reflect the last completed operation; unused providers are healthy, and unattempted required providers are not. The timestamp is the last actual successful serviceURI read.
Standard gRPC health and metrics `/healthz` report liveness, not route readiness.
Use actual Resolve/Select results and
[provider metrics](../design-docs/observability.md) for operational checks.
No heartbeat file or overlay hot-reload handler exists.

Logs use slog with call-site fields such as `addr`, `manifest_url`, `mode`,
capability, offering and `err`. There is no universal correlation_id field.
Audit events are separately queryable through GetAuditLog.

## Shutdown

SIGINT/SIGTERM starts shutdown of listeners and chain seeder. The lifecycle
uses a 10-second drain timeout, though individual graceful-stop operations may
block before that drain. BoltDB closes afterward. A second signal is not
special-cased. SIGHUP is not an overlay reload gesture; restart the process to
load edited configuration.

The metrics listener exposes `/metrics` (configurable), `/healthz`, and a root
index. It has no authentication; bind/expose it according to your deployment.
[Grafana](grafana/README.md) and [Prometheus alerts](prometheus/README.md) are
configurable examples, not proof of healthy chain/discovery state.
