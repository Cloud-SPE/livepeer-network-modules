# orch-coordinator design

Architectural overview for the `orch-coordinator/` component. The full
design rationale lives in
[`../docs/exec-plans/completed/0018-orch-coordinator-design.md`](../docs/exec-plans/completed/0018-orch-coordinator-design.md).
This file is the in-component summary; consult the plan doc for the
design conversation that produced these decisions.

## Boundary

One coordinator process per orch operator. Inputs:

- LAN broker `/registry/offerings` endpoints — HTTP GET, JSON. Shape pinned
  by [`../capability-broker/internal/server/registry/offerings.go`](../capability-broker/internal/server/registry/offerings.go).
- LAN broker `/registry/health` endpoints — HTTP GET, JSON. Used for tuple
  liveness plus broker metadata-discovery status such as `last_result`,
  `consecutive_failures`, and freshness age.
- Static config file `coordinator-config.yaml` — broker list, orch identity,
  tunables.

Outputs:

- The candidate manifest — packaged as a `tar.gz` (`manifest.json` JCS
  bytes + `metadata.json` operator-only sidecar). Operator downloads via
  the web UI.
  The sidecar now includes broker metadata thresholds, per-broker metadata
  summaries, and per-tuple metadata warnings so the operator can evaluate
  degraded discovery state before hand-carrying the candidate for signing.
- The currently-published signed manifest — served on the resolver-facing
  listener at `/.well-known/livepeer-registry.json`.

The coordinator does not hold a key, does not write on-chain pointers, and
does not relax verification. It builds bytes, receives bytes, verifies
bytes, swaps bytes. That's the whole job.

## Data flow

```
        +---------------------------+
        |  capability-broker host A |
        |  /registry/offerings      |
        +------------+--------------+
                     |
                     | HTTP GET
                     v
+--------------------+--------------------+
|              orch-coordinator           |
|                                         |
|  +-----------+  +-----------+           |
|  | scrape    |->| candidate |           |
|  | service   |  | service   |           |
|  +-----------+  +-----+-----+           |
|                       |                 |
|                       v                 |
|                 +-----+-----+           |
|                 |  diff +   |           |
|                 |  roster   |--+        |
|                 +-----------+  |        |
|                                v        |
|                  +-------------+--+     |
|                  | adminapi (web) |     |
|                  +----+-----------+     |
|                       |                 |
|     ssh out / scp     | hand-carry      |
|     to secure-orch    | candidate.tar.gz|
|                       v                 |
|             [secure-orch-console]       |
|                       |                 |
|     POST signed-      | upload          |
|     manifest          v                 |
|                  +----+-----------+     |
|                  | receive svc    |     |
|                  | + verify       |     |
|                  | + atomic-swap  |     |
|                  +----+-----------+     |
|                       |                 |
|                       v                 |
|                  +----+-----------+     |
|                  | published      |     |
|                  | (single file)  |     |
|                  +----+-----------+     |
|                       ^                 |
|                       | GET             |
|                       |                 |
|              [publicapi /.well-known/]  |
|                       ^                 |
+-----------------------|-----------------+
                        |
                        | resolver fetch
```

## Idempotent candidate build

Same broker offerings + same scrape window → byte-identical manifest.

- JCS canonicalization (RFC 8785) for object key ordering.
- `issued_at` is the **scrape window end**, not coordinator wall-clock.
- `expires_at = issued_at + manifest_ttl`.
- Capability tuples sorted by `(capability_id, offering_id, worker_url)`
  before serialization.
- Aggregation rules below dedupe consistently.

## Aggregation rules

The uniqueness key for tuple identity is the canonicalized
`(capability_id, offering_id, extra, constraints)` quadruple. `worker_url`
is **not** part of identity.

Three cases when scraping multiple brokers:

1. **Identical key, different prices.** Operator error. The candidate
   build hard-fails loudly with a structured error citing both broker
   sources and the conflicting prices. No silent precedence rule.
2. **Identical key, identical price, different `worker_url`.** Legitimate
   HA pair. Emit one tuple whose `worker_url` is the lex-min over the
   set; record the others in the metadata sidecar so the operator sees
   them in the roster but does not enter the signed bytes.
3. **Different `extra` or `constraints`.** Distinct identities. Emit
   both as separate tuples; gateway uses these fields for selection.

## Signed-manifest verification

Five steps, run synchronously when an upload arrives:

1. Schema-valid against the manifest schema in
   [`../livepeer-network-protocol/manifest/schema.json`](../livepeer-network-protocol/manifest/schema.json).
2. Signature recovers to the configured `eth_address` via the shared
   verifier in
   [`../livepeer-network-protocol/verify/`](../livepeer-network-protocol/verify/).
3. `manifest.orch.eth_address` matches the configured operator identity.
4. **Schema-version drift check** — reject if the signed manifest's
   `spec_version` differs from the candidate the coordinator most
   recently produced.
5. `issued_at` and `expires_at` well-formed and in the future, plus
   `publication_seq` strictly greater than the currently-published
   manifest's value (rollback defense).

If any step fails, the upload is rejected with a structured error code;
the currently-published manifest stays live.

## Atomic publish

Old manifest stays live until the new one verifies. The publish is
write-tempfile-fsync-rename(2). Single-writer guaranteed by `flock(2)`
over the publish dir; concurrent uploaders block on the lock.

## Persistence

- **In-memory.** Scrape cache (latest broker offerings + per-broker
  status). Recoverable on restart by re-scraping.
  The per-broker status now includes broker `/registry/health` tuple metadata
  so the roster can classify metadata state as `ok`, `degraded`, `stale`, or
  `never_succeeded`.
- **On disk.**
  - `<data-dir>/published/manifest.json` — the live signed manifest.
  - `<data-dir>/candidates/<timestamp>/{manifest.json,metadata.json}` —
    history snapshots; pruned by count.
  - `<data-dir>/audit.db` — BoltDB log of every publish event (uploader,
    timestamp, signature hash, accepted/rejected with reason).

## Observability

- Prometheus surface on `--metrics-listen`. Counters for scrape /
  candidate-build / signed-upload / publish outcomes; histograms for
  durations; gauges for known/healthy brokers, manifest age, tuple
  count, drift count by kind.
- Structured slog audit event on every publish (stable error-code
  strings).

See [`AGENTS.md`](./AGENTS.md) for the runtime configuration surface.
