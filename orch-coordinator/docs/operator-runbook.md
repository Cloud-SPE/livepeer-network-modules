# orch-coordinator operator runbook

## Boot

```
livepeer-orch-coordinator \
  --config=/etc/livepeer/orch-coordinator.yaml \
  --data-dir=/srv/data \
  --listen=:8080 \
  --secure-orch-url=http://secure-orch-laptop:8080 \
  --public-listen=:8081 \
  --metrics-listen=:9091
```

The three listeners are intentionally separate:

- `--listen` — operator UX (web UI + JSON API + signed-manifest upload).
  Bind to a LAN-private interface; this is reachable to operators on the
  same LAN but **must not** be exposed to the public internet.
- `--public-listen` — resolver-facing
  `/.well-known/livepeer-registry.json`. Bind to the public-facing
  interface; only that one path is routed (everything else returns
  404). Defense-in-depth: even if the admin mux gains a new route, it
  cannot leak through this listener.
- `--metrics-listen` — Prometheus `/metrics` plus a `/healthz` probe.

When `ORCH_COORDINATOR_ADMIN_TOKENS` is set, the `--listen` admin surface
requires login. Operators submit an admin token plus `actor`; the UI keeps a
single active session, with a 4-hour absolute timeout and a 30-minute
idle timeout, and upload audit events record that actor. Expired
sessions are released automatically on the next request or login
attempt.

`--agent-token-file` (plan 0042) enables the secure-orch agent's bearer
credential: a file holding a single token that grants `Authorization:
Bearer <token>` access to exactly three routes — `GET /candidate.json`,
`GET /candidate.tar.gz`, and `POST /admin/signed-manifest`. It is a
separate credential from the operator admin tokens so it can be rotated
independently, and it bypasses the single-session login so the agent
never locks out a human operator. Audit events from bearer-admitted
requests record actor `agent`. The bearer only keeps anonymous traffic
off the endpoints; the manifest signature remains the real content
authentication. Keep the file readable only by the coordinator process
(mode `0600`).

`--renewal-threshold` (plan 0042) controls when an unchanged candidate
gets a fresh `issued_at`/`expires_at` so it can be re-signed before the
published manifest expires. Default `0` means one third of the manifest
TTL. With the default 24 h TTL, an unchanged candidate's window
refreshes once its remaining validity drops below 8 h.

The **effective** policy is published in each candidate's
`metadata.json` as `manifest_ttl_seconds` and `renewal_threshold_seconds`
— already defaulted, so a reader never re-derives it. `secure-orch-console`
reads those instead of keeping its own copy (plan 0043 §3.7).

## Spec version

The coordinator's `spec_version` has exactly one source: the protocol
module's `VERSION` constant (`livepeer-network-protocol/version`), which
it imports. Brokers stamp the same constant on `/registry/offerings`.

A broker whose `spec_version` **major** differs from the coordinator's is
refused at the scrape boundary: it is marked `schema_error`, its tuples
are dropped, and the candidate is built without it — a manifest cannot
mix majors, because consumers read the whole document under one
contract. A broker that publishes no `spec_version` at all is refused
the same way: it predates the stamp, so its contract cannot be verified.
The error names both versions, so it is clear which side to upgrade.

When running the published container image, use `/srv/data`. The image is
built to run as `nonroot` and pre-owns that path so Docker named volumes are
initialized with writable ownership.

`--secure-orch-url` is optional; when set, the coordinator checklist can jump
directly to the secure-orch review timeline for the current hand-carry cycle.

## Dev mode

`--dev` boots with synthetic in-memory broker fixtures and a loud
`=== DEV MODE ===` banner. The synthetic config also kicks in if no
`--config` file is found. Use it to smoke-test the binary without
standing up real brokers. Production deployments must NOT pass `--dev`.

## Configuration (YAML)

```yaml
identity:
  orch_eth_address: "0x..."
brokers:
  - name: broker-a
    base_url: http://10.0.0.5:8080
publish:
  manifest_ttl: 24h
```

The orch eth address is the on-chain `ServiceRegistry` (or
`AIServiceRegistry`) entry the cold key on secure-orch will sign for.
The broker list is static for v0.1; service discovery is a follow-up.

## Roster cells

The roster consumes both broker `/registry/offerings` and `/registry/health`.
One row per capability tuple, one cell per broker, so a disagreement between
brokers about the same tuple is visible side by side. Each cell shows that
broker's scrape freshness and its live view of the tuple — `live=ready`,
`live=degraded`, `live=unreachable` — with the broker's own reason as the
cell's tooltip.

Broker metadata-discovery state used to appear here too (`meta=ok`,
`meta=stale`, and a per-broker summary of unhealthy tuple counts). The broker
no longer enriches offerings by polling backends — a runner declares what it
is when it attaches, and certification proves it — so there is no discovery
state left to report.

## Endpoints

### Operator UX (`--listen`)

| Method | Path | Purpose |
|---|---|---|
| GET  | `/candidate.json`        | JCS-canonical manifest bytes (the cold-key inputs) |
| GET  | `/candidate.tar.gz`      | Packaged candidate (manifest.json + metadata.json) |
| POST | `/admin/signed-manifest` | Upload a cold-key-signed manifest (multipart or JSON) |
| GET  | `/healthz`               | process liveness probe (also on the metrics listener) |
| GET  | `/assets/`               | versioned static assets for the web UI |

Web UI (all behind login when `ORCH_COORDINATOR_ADMIN_TOKENS` is set):

| Method | Path | Purpose |
|---|---|---|
| GET      | `/`                        | checklist / landing page |
| GET      | `/roster`                  | capability roster |
| GET      | `/diff`                    | candidate-vs-published diff |
| GET      | `/audit`                   | audit log |
| GET/POST | `/login`, POST `/logout`   | operator session |
| POST     | `/refresh-roster`          | force an out-of-band scrape |
| POST     | `/upload-signed-manifest`  | browser form-post wrapper over `/admin/signed-manifest` |

Both candidate routes return an `ETag` over the candidate's canonical
manifest bytes and honor `If-None-Match` with `304 Not Modified`, so
the secure-orch agent can poll them at ~zero cost (plan 0042 §5.1).
Before the first build they return `503` with `Retry-After`. A `304`
poll is not an audit event; only full tarball downloads are audited.

`metadata.json` is operator-only and not signed. It carries the scrape
window, `source_brokers` with each broker's freshness and last error, the
coordinator commit and schema version, the effective sign policy
(`manifest_ttl_seconds`, `renewal_threshold_seconds`) and `ha_endpoints`.

It no longer carries metadata-discovery fields. The broker stopped
enriching offerings from backends it polled — a runner declares what it
is at attach — so `metadata_warning_threshold_seconds`,
`metadata_stale_threshold_seconds`, `warnings` and
`tuple_metadata_warnings` are gone rather than always empty. A signal
that can never fire is worse than no signal.

### Resolver-facing (`--public-listen`)

| Method | Path | Purpose |
|---|---|---|
| GET | `/.well-known/livepeer-registry.json` | Currently-published signed manifest |

ALL other paths return 404. There is no `/healthz` on this listener;
liveness is checked via `--metrics-listen`'s `/healthz`.

### Metrics (`--metrics-listen`)

| Method | Path | Purpose |
|---|---|---|
| GET | `/metrics` | Prometheus surface |
| GET | `/healthz` | process liveness probe |

Counters:

- `orch_coordinator_scrape_total{broker,outcome}` — `outcome ∈
  {ok, http_error, schema_error, timeout}`.
- `orch_coordinator_candidate_builds_total{outcome}` — `ok / conflict
  / error`.
- `orch_coordinator_signed_uploads_total{outcome}` — accepted /
  schema_invalid / sig_invalid / identity_mismatch / drift_rejected /
  window_invalid / rollback_rejected / publish_failed.
- `orch_coordinator_publishes_total{outcome}` — `accepted` /
  `publish_failed`.

Histograms: scrape / candidate-build / signed-verify wall-clock
durations.

Gauges: `orch_coordinator_known_brokers`,
`orch_coordinator_brokers_healthy`,
`orch_coordinator_published_manifest_age_seconds`,
`orch_coordinator_published_capability_tuples`,
`orch_coordinator_candidate_drift_count{kind}`.

## Failure modes

### Scrape soft failure (broker unreachable / 5xx / timeout)

Broker keeps its last-good entries flagged
`freshness=stale_failing`. Roster surfaces this; candidate is built
unaffected. `orch_coordinator_scrape_total{outcome="http_error"}`
increments.

Action: investigate broker host. The operator may continue signing
and publishing while the soft failure persists; the published
manifest reflects the most-recent successful scrape's state.

### Scrape hard failure (malformed JSON, schema-invalid)

Broker entries are dropped immediately. The next candidate excludes
that broker's tuples. `orch_coordinator_scrape_total{outcome=
"schema_error"}` increments.

Action: if the broker is managed by `pool-controller`, correct the persisted
offer/member/assignment state and re-apply broker runtime. Otherwise, fix the
broker-side `host-config.yaml` or upgrade the broker binary to a compatible
spec version.

### Candidate-build price conflict

Two brokers advertise the same `(capability_id, offering_id, extra,
constraints)` quadruple at different prices. Coordinator hard-fails
the candidate-build pass; the previous candidate stays the
operator's reference point. `orch_coordinator_candidate_builds_total{
outcome="conflict"}` increments and the error appears in the slog
output.

Action: reconcile broker-advertised offer state. In a Pool rollout, fix the
canonical offer state in `pool-controller` and re-apply broker runtime. In a
standalone rollout, reconcile the affected broker `host-config.yaml` files.
Two brokers may not advertise the same identity at different prices.

### Signed-manifest verify rejection

The admin-listener returns the matching HTTP status:

- `400 schema_invalid` — manifest fails the structural check.
- `401 sig_invalid` — signature does not recover to the configured
  eth address, or the signature is structurally malformed.
- `401 identity_mismatch` — `manifest.orch.eth_address` is not the
  configured operator identity.
- `409 drift_rejected` — `spec_version` differs from the
  coordinator's most-recent candidate.
- `409 window_invalid` — `expires_at` is not in the future, or
  `issued_at` is missing.
- `409 rollback_rejected` — `publication_seq <=` currently-published
  value.
- `500 publish_failed` — verify passed but atomic-swap publish
  failed (disk full, lock contention, etc.).

The currently-published manifest stays live across all rejection
codes. Audit log records every attempt with the matching outcome
string.

### Lock held by another writer

A second concurrent uploader sees `ErrLocked`. The coordinator does
not queue uploads; the operator retries. Single-writer guarantee is
intentional — concurrent publishes break the rollback-defense
invariant.
