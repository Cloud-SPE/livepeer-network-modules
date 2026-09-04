# Running the daemon

## Modes

- `--mode=publisher` — orchestrator-side. Loads keystore, builds + signs manifests, and writes signed JSON to disk for operator-managed hosting. On-chain `ServiceRegistry` writes live in `protocol-daemon`.
- `--mode=resolver` — consumer-side. Reads on-chain, fetches + verifies manifests, serves resolved nodes via gRPC.

A single host can run both daemons side-by-side with separate sockets and stores; they don't interact.

## Common flags

| Flag | Default | Notes |
|---|---|---|
| `--mode` | (required) | `publisher` or `resolver` |
| `--socket` | `/var/run/livepeer-service-registry.sock` | Unix socket path for gRPC |
| `--store-path` | `/var/lib/livepeer/registry-cache.db` | BoltDB file (resolver only by default; publisher also uses it for write history) |
| `--chain-rpc-urls` | _(required outside `--dev`)_ | Comma-separated Ethereum JSON-RPC endpoints, primary first; the daemon fails over between them. No built-in default: the daemon refuses to start without it. |
| `--chain-id` | `42161` | Sanity check; daemon refuses to boot if RPC reports a different chain |
| `--controller-address` | `0xD8E8...6ee4` (Arbitrum One) | Livepeer Controller. Resolver derives `BondingManager` + `RoundsManager` from it, and `ServiceRegistry` too when `--service-registry-address` is empty. |
| `--service-registry-address` | `""` | Optional override for the primary registry contract used by resolver `getServiceURI()` lookups. When empty, the resolver reads `ServiceRegistry` from Controller. |
| `--ai-service-registry-address` | `0x04C0...` (Arbitrum One) | Optional AI registry fallback. Resolver consults it when the primary registry has no pointer for the address. |
| `--log-format` | `text` | `text` or `json` |
| `--log-level` | `info` | `debug`, `info`, `warn`, `error` |

## Resolver-only flags

| Flag | Default | Notes |
|---|---|---|
| `--discovery` | `chain` | `chain` walks BondingManager pool on each round event (auto-discovery; default). `overlay-only` disables auto-discovery; the daemon walks `--static-overlay` once at startup and pre-resolves each enabled entry so `ListKnown` / `Select` return the operator-curated pool without per-consumer `Refresh` calls. |
| `--round-poll-interval` | `1m` | How often the chain-commons timesource polls `RoundsManager.currentRound()` to detect round transitions. Bounds detection latency for cache refreshes; ~19 hour rounds make 1 minute plenty. |
| `--cache-manifest-ttl` | `600s` | Reuse fetched manifest for this long. Independent of round-anchored chain refreshes. |
| `--manifest-max-bytes` | `4194304` (4 MiB) | Hard cap on manifest body size; operator-tunable up to 16 MiB |
| `--manifest-fetch-timeout` | `5s` | HTTP timeout per fetch attempt |
| `--max-stale` | `1h` | After this, last-good is dropped and `cache_stale_failing` is returned |
| `--static-overlay` | `""` (none) | Path to operator-curated `nodes.yaml`. Layered on top of chain discovery (overlay wins on policy fields like `enabled` / `tier_allowed` / `weight`). |
| `--reject-unsigned` | `true` | If `false`, unsigned manifests (CSV-mode) are returned without `allow_unsigned=true` per request |
| `--worker-probe-timeout` | `5s` | HTTP timeout for the live-health probe of a worker's `/registry/health` during `Select` / `SelectMany`. Despite the name this is **not** publisher-only — it is on the resolver request path. |

The previously-documented `--cache-chain-ttl` flag was removed in plan 0009 §C
(2026-04-27) when chain-side cache invalidation switched from a fixed TTL to
round-anchored refreshes via `chain-commons.services.roundclock`.

## Publisher-only flags

| Flag | Default | Notes |
|---|---|---|
| `--keystore-path` | (required) | V3 JSON keystore for the orchestrator's eth key |
| `--keystore-password-file` | (or `LIVEPEER_KEYSTORE_PASSWORD` env) | Password for the keystore |
| `--orch-address` | (derived from keystore) | Override for hot/cold split (advanced) |
| `--worker-probe-timeout` | `5s` | Resolver-side worker probing (see the resolver table). |

## Metrics flags (both modes)

| Flag | Default | Notes |
|---|---|---|
| `--metrics-listen` | `""` (off) | TCP `host:port` for the Prometheus `/metrics` listener. Empty = no listener. |
| `--metrics-path` | `/metrics` | URL path the handler is bound to. |
| `--metrics-max-series-per-metric` | `10000` | Cardinality cap. New label tuples beyond this are logged + dropped. `0` disables the cap. |

Sample `prometheus.yml` scrape config (for `--metrics-listen=:9091`):

```yaml
scrape_configs:
  - job_name: livepeer-service-registry
    scrape_interval: 15s
    static_configs:
      - targets: ['registry-host:9091']
        labels:
          mode: resolver   # or publisher
```

The `/healthz` endpoint on the same listener returns plain-text `ok` for k8s/HTTP liveness probes that prefer not to use gRPC health-checking.

Full metric catalog: [`docs/design-docs/observability.md`](../design-docs/observability.md).

A pre-built Grafana dashboard covering the catalog ships in [`docs/operations/grafana/`](grafana/) — drop the JSON into Grafana's import dialog and pick your Prometheus datasource. UID is `livepeer-service-registry`, so re-imports update in place.

A matching set of Prometheus alert rules ships in [`docs/operations/prometheus/`](prometheus/) — three severity tiers (`page` / `ticket` / `info`), twelve alerts. Drop into your `rule_files:` and reload Prometheus.

## Dev mode

Set `--dev` on either mode. Effects:
- All providers are replaced with in-memory fakes (Chain, Signer, Verifier, Store).
- A throwaway eth key is generated at boot (publisher mode).
- A loud `=== DEV MODE ===` banner prints to stderr.
- Manifest fetcher accepts `http://localhost:*` URLs.

`--dev` and `--chain-rpc-urls` are mutually exclusive.

## Health

Three liveness surfaces, all real:

- the gRPC `Resolver.Health()` / `Publisher.Health()` methods;
- the standard `grpc.health.v1.Health` service (plus server reflection) on the same socket;
- plain-text `GET /healthz` on the metrics listener, when `--metrics-listen` is set.

There is **no** heartbeat file. Earlier drafts of this page described a
`daemon.alive` file next to `--store-path`; the daemon has never written
one, so do not build a file-mtime probe around it.

## Shutdown

SIGTERM / SIGINT triggers graceful shutdown:
1. Stop accepting new gRPC requests (and stop the metrics listener + chain seeder).
2. Wait up to 10s for the serve goroutines to drain.
3. Close BoltDB, release file locks.
4. Exit 0.

Only SIGINT and SIGTERM are handled. A second signal during the drain is
not special-cased — it does not shorten the wait. Any other signal
(SIGHUP included) takes the Go runtime default and kills the process.

## Logging

`slog` structured logging. Every entry includes `mode`, `eth_address` (when applicable), and `correlation_id`. Examples:

```
INFO mode=resolver eth_address=0xabcd...0123 event=manifest_fetched bytes=1842 cache_hit=false took=124ms
WARN mode=resolver eth_address=0xabcd...0123 event=signature_invalid recovered=0xff... claimed=0xab...
ERROR mode=publisher event=chain_write_failed err="chain_write_not_implemented"
```

## Hermetic runs that need signatures (`--chain-seed`)

There are two chain-free ways to run a resolver, and they are not
interchangeable.

**`--discovery=overlay-only`** serves the pin nodes in `--static-overlay`.
Those pins are *operator-asserted and unsigned by construction*: the
resolver marks them `unsigned`, and they carry **no settlement
delegation**. That is deliberate. `settlement_keys` say which hot keys are
authorized to sign settlement records, and their authority comes from the
orchestrator's cold key signing the manifest that lists them. A YAML file
on the resolver host asserting the same thing establishes nothing — it is
precisely the claim the signature exists to make. So overlay-only is right
for routing and capacity curation, and wrong for anything a clearinghouse
will settle against.

**`--chain-seed`** is the path when signatures matter. It preloads the
in-memory chain (so it requires `--dev`) with address → serviceURI pairs:

```yaml
# seed.yaml
seed:
  - eth_address: "0xabc0000000000000000000000000000000000000"
    service_uri: "http://127.0.0.1:9099/.well-known/livepeer-registry.json"
```

```
livepeer-service-registry-daemon --mode=resolver --dev \
  --chain-seed ./seed.yaml --socket /tmp/registry.sock
```

Serve a **signed** manifest at that URI from any static file server, and
resolution takes the ordinary well-known path: fetch, canonicalize, verify
the signature, project `settlement_keys` onto every node from that
address. Nothing is stubbed except where the serviceURI came from, which
is the one thing a chain-free run cannot have.

This is the supported seed path for nightly CI that needs signed
settlements end to end. `--chain-seed` is refused without `--dev`, and
refused alongside `--discovery=overlay-only` — overlay-only never reads
the chain, so the seed would be silently ignored.

To produce the signed manifest itself, run the daemon in publisher mode or
see `examples/minimal-e2e`, which builds and signs one in-process.

## Overlay-only seed-on-startup

When `--mode=resolver` runs with `--discovery=overlay-only` (forced in `--dev` mode), the daemon walks every enabled entry in `--static-overlay` once at startup and calls `ResolveByAddress` for each. Two paths can succeed:

- **Chain has the address** — the resolver fetches the manifest (or synthesizes legacy from the chain URI). Production overlay-only deployments land here.
- **Chain has no entry** — the resolver falls into the `static-overlay` synth path and serves the overlay's pin nodes (see [`docs/design-docs/serviceuri-modes.md`](../design-docs/serviceuri-modes.md) §"Mode D"). `--dev` and the `static-overlay-only` example land here.

After seed completes, `ListKnown` and `Select` reflect the full pool. Per-address seed errors are logged at `WARN` and skipped — a single missing manifest does not block the rest of the seed.

## Examples

End-to-end demo: see `examples/minimal-e2e/`.

Static-overlay-only resolution (no chain RPC needed): see `examples/static-overlay-only/`.
