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
| `--chain-rpc` | `https://arb1.arbitrum.io/rpc` | Ethereum JSON-RPC endpoint |
| `--chain-id` | `42161` | Sanity check; daemon refuses to boot if RPC reports a different chain |
| `--service-registry-address` | `0xC92d...` (Arbitrum One) | Registry contract used by resolver `getServiceURI()` lookups. Gateway deployments should set this explicitly. |
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

The previously-documented `--cache-chain-ttl` flag was removed in plan 0009 §C
(2026-04-27) when chain-side cache invalidation switched from a fixed TTL to
round-anchored refreshes via `chain-commons.services.roundclock`.

## Publisher-only flags

| Flag | Default | Notes |
|---|---|---|
| `--keystore-path` | (required) | V3 JSON keystore for the orchestrator's eth key |
| `--keystore-password-file` | (or `LIVEPEER_KEYSTORE_PASSWORD` env) | Password for the keystore |
| `--orch-address` | (derived from keystore) | Override for hot/cold split (advanced) |
| `--manifest-out` | `""` | If set, the daemon writes the signed `registry-manifest.json` here whenever `SignManifest` is invoked. Operator's HTTP server serves this file at the exact URL later published on-chain. |
| `--worker-probe-timeout` | `5s` | Reserved for the deferred `ProbeWorker` implementation; the gRPC method is currently unimplemented. |

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

`--dev` and `--chain-rpc` are mutually exclusive.

## Health

The daemon exposes a gRPC `Health()` method; in addition, it writes a heartbeat file at `--store-path` sibling `daemon.alive` updated every 5s (operators can rely on file-mtime for liveness if gRPC is locked behind firewalls in their setup).

## Shutdown

SIGTERM / SIGINT triggers graceful shutdown:
1. Stop accepting new gRPC requests.
2. Wait up to 10s for in-flight requests.
3. Flush BoltDB, release file locks.
4. Exit 0.

A second signal forces immediate exit (1).

## Logging

`slog` structured logging. Every entry includes `mode`, `eth_address` (when applicable), and `correlation_id`. Examples:

```
INFO mode=resolver eth_address=0xabcd...0123 event=manifest_fetched bytes=1842 cache_hit=false took=124ms
WARN mode=resolver eth_address=0xabcd...0123 event=signature_invalid recovered=0xff... claimed=0xab...
ERROR mode=publisher event=chain_write_failed err="chain_write_not_implemented"
```

## Overlay-only seed-on-startup

When `--mode=resolver` runs with `--discovery=overlay-only` (forced in `--dev` mode), the daemon walks every enabled entry in `--static-overlay` once at startup and calls `ResolveByAddress` for each. Two paths can succeed:

- **Chain has the address** — the resolver fetches the manifest (or synthesizes legacy from the chain URI). Production overlay-only deployments land here.
- **Chain has no entry** — the resolver falls into the `static-overlay` synth path and serves the overlay's pin nodes (see [`docs/design-docs/serviceuri-modes.md`](../design-docs/serviceuri-modes.md) §"Mode D"). `--dev` and the `static-overlay-only` example land here.

After seed completes, `ListKnown` and `Select` reflect the full pool. Per-address seed errors are logged at `WARN` and skipped — a single missing manifest does not block the rest of the seed.

## Examples

End-to-end demo: see `examples/minimal-e2e/`.

Static-overlay-only resolution (no chain RPC needed): see `examples/static-overlay-only/`.
