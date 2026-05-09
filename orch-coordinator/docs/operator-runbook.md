# orch-coordinator operator runbook

## Boot

```
livepeer-orch-coordinator \
  --config=/etc/livepeer/orch-coordinator.yaml \
  --data-dir=/srv/data \
  --listen=:8080 \
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
single active session and upload audit events record that actor.

When running the published container image, use `/srv/data`. The image is
built to run as `nonroot` and pre-owns that path so Docker named volumes are
initialized with writable ownership.

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

## Endpoints

### Operator UX (`--listen`)

| Method | Path | Purpose |
|---|---|---|
| GET  | `/candidate.json`        | JCS-canonical manifest bytes (the cold-key inputs) |
| GET  | `/candidate.tar.gz`      | Packaged candidate (manifest.json + metadata.json) |
| POST | `/admin/signed-manifest` | Upload a cold-key-signed manifest (multipart or JSON) |

Web UI routes (`/`, `/diff`, `/audit`) land in plan 0018 commit 6.

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

Action: fix broker-side `host-config.yaml` or upgrade broker binary
to a compatible spec version.

### Candidate-build price conflict

Two brokers advertise the same `(capability_id, offering_id, extra,
constraints)` quadruple at different prices. Coordinator hard-fails
the candidate-build pass; the previous candidate stays the
operator's reference point. `orch_coordinator_candidate_builds_total{
outcome="conflict"}` increments and the error appears in the slog
output.

Action: reconcile broker `host-config.yaml` files. Two brokers may
not advertise the same identity at different prices.

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
