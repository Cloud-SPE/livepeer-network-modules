# Plan 0018 — orch-coordinator design

**Status:** design-doc (pure paper; no code, no `go.mod` edits)
**Opened:** 2026-05-06
**Owner:** harness
**Related:** plan 0003 (capability-broker, closed), plan 0014 (wire-compat
payment, closed); roadmap row Phase 3 in [`PLANS.md`](../../../PLANS.md)
line 108.

## 1. Status + scope

This is the opening design pass for `orch-coordinator/` — a component
that does not yet exist in this monorepo. The roadmap row tracking it
(`PLANS.md` line 108) reads: "Coordinator UX rework — capability-as-roster-entry —
`orch-coordinator/` — not started."

**What the coordinator owns:**

- LAN scrape of one or more capability-broker `/registry/offerings`
  endpoints
  ([`capability-broker/internal/server/registry/offerings.go`](../../../capability-broker/internal/server/registry/offerings.go)
  lines 26–73).
- Building a **candidate manifest** payload conforming to
  [`livepeer-network-protocol/manifest/schema.json`](../../../livepeer-network-protocol/manifest/schema.json)
  `#/$defs/manifest` (lines 16–45) — flat list of capability tuples
  rooted on the orch's `eth_address`.
- Hosting that candidate for the operator to hand-carry to secure-orch.
- Receiving the cold-key-signed manifest back, verifying it, and
  atomic-swap publishing under the resolver-facing URL.
- Exposing the operator-facing roster UX (per-tuple, not per-host).

**What the coordinator does NOT own:**

- Trust decisions. Cold key on `secure-orch` is the only signer
  (core belief #4, [`docs/design-docs/core-beliefs.md`](../../design-docs/core-beliefs.md)
  lines 28–35).
- Inbound traffic from the public internet for scrape — it pulls from
  brokers on its own LAN.
- The resolver. Resolver/publisher split in
  `livepeer-modules-project/service-registry-daemon/` keeps working
  (architecture-overview.md line 133).
- Workload semantics. Capability strings are opaque (R7,
  [`docs/design-docs/requirements.md`](../../design-docs/requirements.md)
  lines 67–73).
- On-chain `ServiceRegistry` writes — `protocol-daemon` owns those
  per the prior impl's `service-registry-daemon/README.md` line 78–80.
- Automated push of signed manifests to secure-orch (R11
  hand-carry stays).

## 2. What's already settled

The architecture-overview pins these. We do not relitigate them in this
plan.

- Manifest data model: flat tuple list, `host` is not a registration
  unit ([`docs/design-docs/architecture-overview.md`](../../design-docs/architecture-overview.md)
  lines 124–134).
- Operator-driven sign cycle: edit → broker re-advertises → coordinator
  scrapes → coordinator builds candidate → operator pulls to
  secure-orch → cold key signs → operator pushes back → coordinator
  atomic-swap publishes (architecture-overview.md lines 140–155).
- Secure-orch never accepts inbound (architecture-overview.md line 138;
  core belief #4).
- Coordinator does NOT make trust decisions — cold key does.
- Coordinator does NOT take inbound from the public internet for
  scraping; it pulls from its own LAN brokers (architecture-overview.md
  line 67 + line 31 box).
- The existing `service-registry-daemon` resolver/publisher split keeps
  working; what changes is the manifest schema and the coordinator UX
  (architecture-overview.md line 133).

## 3. Component boundary

One coordinator process per orch operator (not per host). A single
operator with multiple broker hosts on the LAN runs one coordinator;
the coordinator scrapes them all and unifies their offerings into a
single candidate manifest.

**Inputs:**

- LAN broker `/registry/offerings` endpoints (HTTP GET, JSON, shape
  defined in `capability-broker/internal/server/registry/offerings.go`
  lines 35–53).
- Optionally `/registry/health` for the live-availability signal
  (`capability-broker/internal/server/registry/health.go` lines 19–33).
- Static config file (`coordinator-config.yaml`) listing the broker
  hosts, the orch's `eth_address`, and tunables.

**Outputs:**

- (a) **Candidate manifest blob** — a tarball or JSON file hosted at a
  download endpoint the operator pulls to secure-orch
  (architecture-overview.md line 144–146).
- (b) **Signed-manifest hosting endpoint** — the URL referenced by the
  on-chain `ServiceRegistry.serviceURI`. Resolvers fetch from here.

**Sub-boundary against the prior impl.** The existing
`livepeer-modules-project/service-registry-daemon/` ships a publisher
service (`internal/service/publisher/publisher.go` lines 79–164) that
**builds and signs in-process** with a hot keystore on disk. That code
path is **the wrong shape for this architecture** — the rewrite splits
build-then-sign across two hosts (coordinator builds; secure-orch
signs). The coordinator wraps/replaces only the candidate-build, the
hosting, and the UX. The prior repo's resolver
(`internal/service/resolver/resolver.go`) is reused as-is by external
consumers; it just fetches the URL the coordinator hosts.

**Not in scope of plan 0018:** `secure-orch-console/` (separate
component, separate plan; the coordinator just hands it bytes).

## 4. Data model migration

The architecture-overview pins the target tuple shape
(architecture-overview.md lines 125–127):

```
(capability_id, offering_id, interaction_mode, work_unit_name,
 price_per_unit_wei, worker_url, eth_address, extra, constraints)
```

The new monorepo's authoritative schema is
[`livepeer-network-protocol/manifest/schema.json`](../../../livepeer-network-protocol/manifest/schema.json)
(`#/$defs/capability` at lines 66–117). Required fields:
`capability_id`, `offering_id`, `interaction_mode`, `work_unit`,
`price_per_unit_wei`, `worker_url`. Optional: `extra`, `constraints`.

**Compared to the prior impl** at
`livepeer-modules-project/service-registry-daemon/internal/types/manifest.go`
lines 27–70:

| Concern | Prior shape (v3.0.1) | New shape (rewrite) |
|---|---|---|
| Outer envelope | `Manifest{schema_version, eth_address, issued_at, nodes[], signature}` | `{manifest{spec_version, issued_at, expires_at, orch{eth_address}, capabilities[]}, signature}` |
| Registration unit | `Node` (host) — `nodes[].capabilities[].offerings[]` 3-deep | flat `capabilities[]` tuple list |
| Pricing | `Offering.price_per_work_unit_wei` (nested under capability under node) | top-level on the tuple |
| Signature alg | `eth-personal-sign` | `secp256k1` (raw) — schema.json lines 137–144 |
| Canonicalization | bespoke (see prior `types/canonical.go`) | RFC 8785 JCS (schema.json line 152) |
| Expiration | none (only `issued_at`) | `expires_at` required (schema.json lines 32–35) |
| Mode tag | not carried | `interaction_mode` per tuple (architecture-overview.md line 131) |

**Migration spec for the coordinator:**

- Coordinator emits **only** the new flat-tuple shape. Schema version
  pinned to whatever the spec repo declares (currently `0.1.0` per
  `livepeer-network-protocol/manifest/examples/minimal.json` line 3).
- No reverse compat with the prior 3-deep shape — core belief #13
  ("no backwards-compatibility shims for the old worker shape",
  core-beliefs.md lines 86–90).
- Validator rules at the coordinator boundary mirror the boundary-decoder
  pattern from the prior impl (`internal/types/decoder.go`): a single
  function ingests broker `/registry/offerings` responses and decodes
  them into typed tuples, rejecting on schema, eth-address case, URL
  scheme, decimal-string price.
- Transition window: N/A. The rewrite has no live deployments yet.
  When this lands, brokers and coordinator ship together.

## 5. Scrape pipeline

Concrete behaviors:

**Discovery.** Static config initially. The coordinator reads
`coordinator-config.yaml` listing each broker by hostname and base URL
(LAN-private; HTTPS not required on the LAN — orch's choice). Service
discovery (DNS-SD, Consul, Kubernetes Service) is a follow-up plan.

**Scrape cadence.** Recommend **every 30 seconds**, configurable via
`--scrape-interval`. Matches the live-availability cadence the broker's
health endpoint targets ("gateway resolvers poll every 15-30s",
`capability-broker/internal/server/registry/health.go` line 14).

**Per-broker timeout.** Recommend **5s** for `/registry/offerings`,
configurable via `--scrape-timeout`. Brokers exceeding it are treated
as offline for that scrape cycle.

**Failure handling.** Two-tier:

- **Soft fail (broker unreachable, 5xx, timeout):** coordinator keeps
  the broker's last-good entries in the candidate, marks them
  `freshness=stale_failing` in the roster UX, increments a Prometheus
  counter. Same "last-good fallback" pattern the prior resolver uses
  (`livepeer-modules-project/service-registry-daemon/README.md` line 158).
- **Hard fail (broker returns malformed JSON or schema-invalid data):**
  coordinator drops the broker's entries from the candidate
  immediately, logs structured error, increments a separate counter.
  Schema poisoning is louder than network blips.

**Per-broker freshness window.** Capabilities older than `N` seconds
(default `5 × scrape-interval` = 150s) are dropped from the candidate.
Operator override via `--freshness-window`.

**Aggregation rules — deduplication.** Two brokers may advertise the
same `(capability_id, offering_id)`. Three options for v0.1; **see
open question Q2 in §14**:

1. **Reject ambiguous candidates** — coordinator refuses to build,
   surfaces a config error to the operator.
2. **First-wins by lexicographic broker name** — deterministic, but
   silently masks duplicate-with-different-prices bugs.
3. **Emit both as separate tuples** — the manifest has no uniqueness
   constraint on `(capability_id, offering_id)`; downstream resolvers
   tolerate it as load-balancing across hosts.

Recommend (3) — fits "host is not a registration unit"
(architecture-overview.md line 127); duplicate-tuples-with-different-`worker_url`
is **the intended encoding** for multi-broker hosts.

## 6. Candidate-build pipeline

What the coordinator hands the secure-orch operator.

**Binary format.** Recommend **tarball** with two members:

- `manifest.json` — JCS-canonicalized manifest payload (no signature
  yet). Byte-identical to what `secure-orch` will sign. This is the
  load-bearing constraint: the cold key signs whatever the coordinator
  built, byte-for-byte, so the diff the operator sees in
  `secure-orch-console` is a diff of the same bytes that hash and
  sign.
- `metadata.json` — non-signed sidecar with: candidate timestamp,
  scrape window (start+end), source broker URLs and their per-broker
  scrape success status, coordinator git commit, schema version.

(Open question Q3 in §14: tarball vs single-file JSON+sig. Recommend
tarball because metadata is operator-useful but must not enter the
signed bytes.)

**Diffing surface.** Coordinator MUST show the operator a diff against
the currently-published manifest **before** they hand-carry to
secure-orch. Reason: short-circuit no-op trips (operator updates
`host-config.yaml`, restarts broker, nothing materially changed →
operator sees "no diff" and skips the cold-key trip). This is a UX
primitive in the coordinator, distinct from (and a precursor to) the
authoritative diff that `secure-orch-console` shows on the air-gapped
side.

**Idempotent builds.** Same inputs (same broker offerings, same
freshness window, same schema version) MUST produce the same bytes.
Required for the secure-orch console's diff confidence (otherwise
operators see noise diffs and start ignoring real changes). Concretely:

- JCS canonical key ordering (RFC 8785) — already in the spec
  (manifest/schema.json line 152).
- `issued_at` is the **scrape window end**, not coordinator wall-clock,
  so re-running the build over the same scrape window yields the same
  timestamp.
- `expires_at` derived from `issued_at + ttl` where `ttl` is config.
- Capability tuples sorted by `(capability_id, offering_id, worker_url)`
  before serialization.

## 7. Signed-manifest hosting (receive-and-republish)

**Upload surface.** Operator uploads the signed manifest back. Two
recommended channels:

- HTTP `POST /admin/signed-manifest` (multipart upload, mTLS or shared
  secret). Suitable for headless / scripted operators.
- Filesystem drop at `/var/lib/livepeer/orch-coordinator/inbox/`,
  picked up by an inotify watcher. Suitable for SCP / USB delivery.

Recommend **both**; they share the same verify+publish pipeline.

**Verification before publishing.** The coordinator MUST verify:

1. Schema-valid against `livepeer-network-protocol/manifest/schema.json`
   (top-level wrapper).
2. Signature recovers to the configured `eth_address`. (Same recover
   logic the prior impl uses in
   `livepeer-modules-project/service-registry-daemon/internal/providers/verifier/`.)
3. `manifest.orch.eth_address` matches the configured operator
   identity.
4. **Schema-version drift check** — reject if the signed manifest's
   `spec_version` differs from the candidate the coordinator most
   recently produced. Catches stale uploads + spec-mismatch attacks.
5. `issued_at` and `expires_at` are well-formed and in the future.

If verification fails, the upload is rejected with a structured error
code; the currently-published manifest stays live. No silent failure.

**Atomic swap.** Old manifest stays live until the new one verifies.
Implementation pattern: write to a tempfile in the same filesystem,
`fsync`, then `rename(2)` to the live path. Single-writer guarantee
enforced by `flock(2)` over the publish directory; second uploader
waits or fails-fast (config).

**Resolver-facing endpoint.** The coordinator exposes
`GET /.well-known/livepeer-registry.json` (the URL pattern the spec
pins, `livepeer-network-protocol/manifest/README.md` lines 4 + 34).
Open question Q4 in §14: same path semantics as the prior impl, or a
new path? Recommend same path so existing resolvers point at the new
coordinator with no consumer-side change.

## 8. UX surface — capability-as-roster-entry

The operator-facing UI. The roadmap row's outcome statement
(`PLANS.md` line 108) names this work.

**Tech choice.** Three options; **see open question Q1 in §14**:

1. JSON HTTP API + a thin CLI (`orch-coordinator-cli`). Mirrors the
   payment-daemon pattern (cmd-line + sockets + slog;
   `payment-daemon/cmd/livepeer-payment-daemon/main.go` lines 37–86).
2. JSON HTTP API + a small embedded web app (single static SPA, no
   external server).
3. Both.

Recommend **(1) for v0.1** — ships fast, no JS toolchain in the build,
no auth-surface design needed beyond the existing socket / mTLS gating.
The web app lands in a follow-up plan (open question: required for
plan 0018 v0.1?).

**The roster view (rows = capability tuples).**

```
+------------------------------------+--------------+-------------+--------------+----------------+--------+
| capability_id / offering_id        | mode         | price_wei   | broker(s)    | published?     | drift  |
+------------------------------------+--------------+-------------+--------------+----------------+--------+
| openai:chat-completions:llama-3-70b| http-stream  | 1500000     | broker-a,b   | yes            | none   |
|   vllm-h100-batch4                 |   @v1        |             |              |                |        |
| video:transcode.live.rtmp          | rtmp-...     | 200000      | broker-c     | yes            | price  |
|   h264-1080p30                     |   @v1        |             |              |                | (was   |
|                                    |              |             |              |                |  180k) |
+------------------------------------+--------------+-------------+--------------+----------------+--------+
```

**Per-row data:**

- Tuple identity (capability_id, offering_id).
- Interaction mode (with `@vN`).
- Price.
- Which broker(s) advertise it (1+).
- Currently-published value (from the live manifest).
- Drift indicator: would the candidate change this row?
  (none / price / mode / extra / new / removed)
- Per-broker freshness/health badge.

**Filtering / search.** By `capability_id` substring, by mode, by
broker, by drift status (e.g. "show me only rows the next publish would
change"). Required UX — the roster scales to many tuples.

**Mutating UX.** Read-only first. Editing `host-config.yaml` from the UI
is **out of scope for plan 0018 v0.1**. Operator edits config on the
broker host; coordinator scrapes the result. Reduces the v0.1 surface
substantially.

## 9. Persistence

What state lives where:

**In-memory.** Scrape cache (latest broker offerings + per-broker
status). Recoverable on restart by re-scraping. NOT durable.

**On-disk.**

- **Currently-published signed manifest.** A single file on disk; the
  resolver-facing endpoint serves bytes from here.
- **Candidate manifest snapshots — history.** Last `N` candidates
  (default 50) for diff / forensic comparison. Filesystem under
  `/var/lib/livepeer/orch-coordinator/candidates/` with timestamped
  names; old entries pruned by count or age.
- **Audit log.** Every publish event: who uploaded, when, signature
  hash, candidate hash diff, result (accepted / rejected with reason).
  Recommend **BoltDB** — same choice the payment-daemon receiver uses
  for the session ledger
  (`payment-daemon/cmd/livepeer-payment-daemon/main.go` line 41) and
  the prior impl's resolver cache uses for audit
  (`livepeer-modules-project/service-registry-daemon/internal/repo/audit/`).

Single-writer guarantee on the publish directory via `flock`.

## 10. Observability

Prometheus surface mirroring core belief #9 / R10. Counters,
histograms, gauges — cardinality-capped so a malicious or buggy
broker can't blow up the metrics endpoint.

**Counters:**

- `orch_coordinator_scrape_total{broker,outcome}` —
  outcome ∈ {ok, timeout, http_error, schema_error}.
- `orch_coordinator_candidate_builds_total{outcome}`.
- `orch_coordinator_signed_uploads_total{outcome}` —
  outcome ∈ {accepted, schema_invalid, sig_invalid, drift_rejected,
  identity_mismatch}.
- `orch_coordinator_publishes_total{outcome}`.

**Histograms:**

- `orch_coordinator_scrape_duration_seconds{broker}`.
- `orch_coordinator_candidate_build_duration_seconds`.
- `orch_coordinator_signed_verify_duration_seconds`.

**Gauges:**

- `orch_coordinator_known_brokers` — total brokers configured.
- `orch_coordinator_brokers_healthy` — currently-reachable.
- `orch_coordinator_published_manifest_age_seconds`.
- `orch_coordinator_published_capability_tuples`.
- `orch_coordinator_candidate_drift_count{kind}` — pending-publish
  changes by kind (added / removed / price-changed / mode-changed).

**Audit log.** Every publish gets a structured slog event with stable
error-code strings (mirrors the prior impl's audit pattern,
`livepeer-modules-project/service-registry-daemon/README.md` line 161).

## 11. Configuration

Mirror the payment-daemon pattern
(`payment-daemon/cmd/livepeer-payment-daemon/main.go` lines 37–86):

| Flag / env | Purpose |
|---|---|
| `--config=/etc/livepeer/orch-coordinator.yaml` | structured config (broker list, identity, tunables) |
| `--dev` | dev mode: in-memory store, fake brokers, loud `=== DEV MODE ===` banner |
| `--listen=:8080` | operator UX HTTP API |
| `--public-listen=:8081` | resolver-facing `/.well-known/...` |
| `--metrics-listen=:9091` | Prometheus |
| `--data-dir=/var/lib/livepeer/orch-coordinator` | history + audit + currently-published manifest |
| `--log-level={debug,info,warn,error}` | slog |
| `--log-format={text,json}` | slog |
| `--scrape-interval=30s` | scrape cadence |
| `--scrape-timeout=5s` | per-broker timeout |
| `--freshness-window=150s` | drop-stale-tuples threshold |
| `--manifest-ttl=24h` | `expires_at = issued_at + ttl` |
| `--version` | print version and exit |

Config file shape (YAML):

```yaml
identity:
  orch_eth_address: 0xabc...
brokers:
  - name: broker-a
    base_url: http://10.0.0.5:8080
  - name: broker-b
    base_url: http://10.0.0.6:8080
publish:
  manifest_ttl: 24h
  signed_inbox_dir: /var/lib/livepeer/orch-coordinator/inbox
```

## 12. Component layout

Proposed `orch-coordinator/` directory structure (mirrors
`payment-daemon/` and `capability-broker/` from the monorepo
convention):

```
orch-coordinator/
  AGENTS.md                           # repo-local agent map
  CLAUDE.md                           # one-liner pointer to AGENTS.md
  DESIGN.md                           # full architecture for this component
  README.md                           # operator quick-start
  Makefile                            # build / test / shell / publish
  Dockerfile                          # distroless static, tztcloud/orch-coordinator
  go.mod / go.sum
  cmd/
    livepeer-orch-coordinator/
      main.go                         # flags, wiring, signal handling
  internal/
    config/                           # YAML parser + validation
    types/                            # decoded broker offerings, candidate, signed manifest
    providers/                        # cross-cutting I/O behind interfaces
      brokerclient/                   # HTTP GET /registry/offerings (real + dev fake)
      verifier/                       # secp256k1 recover + JCS canonical compare
      clock/                          # System + Fixed
      logger/                         # slog wrapper
    repo/
      candidates/                     # filesystem snapshots
      audit/                          # BoltDB
      published/                      # currently-live manifest store
    service/
      scrape/                         # poll loop, freshness, dedup, last-good fallback
      candidate/                      # build canonical bytes from scrape cache
      receive/                        # verify + atomic-swap publish
      diff/                           # candidate-vs-published diff for UX + console pre-trip
      roster/                         # roster view materialization for UX
    server/
      adminapi/                       # operator-facing HTTP+JSON
      publicapi/                      # resolver-facing /.well-known/...
      metrics/                        # Prometheus
  docs/
    design-docs/
    exec-plans/
      active/
      completed/
    operator-runbook.md
    references/
  examples/
    coordinator-config.yaml
  scripts/
```

Layer rule mirrors the prior impl: `types → config → repo → service →
server`, all I/O through `internal/providers/`. Mechanical enforcement
via `depguard` (same pattern as
`livepeer-modules-project/service-registry-daemon/`).

## 13. Migration sequence

Recommend 4–6 commits, each independently shippable as a milestone:

1. **Scaffold + scrape only.** Component skeleton, config parser,
   broker HTTP client, scrape loop, in-memory candidate cache.
   `--dev` mode boots and scrapes a fake broker. **No candidate output
   yet.** Lands the directory layout (§12) and the config surface
   (§11) early so other plans can pin against them.
2. **Candidate build.** JCS canonicalization, idempotent build,
   filesystem snapshot, history pruning. CLI command to dump the
   current candidate to stdout.
3. **Diff surface + roster materialization.** Candidate-vs-published
   diff computation, roster view assembly. Still no inbound from
   secure-orch.
4. **Signed-manifest hosting.** Receive-and-verify pipeline (HTTP
   upload + filesystem drop), signature recovery, schema-drift check,
   atomic swap to live, audit log.
5. **Resolver-facing endpoint + Prometheus.** `/.well-known/...`
   served from the live store, full metrics surface, structured
   logging, runbook draft.
6. **(Optional) Web roster UI.** Embedded SPA. May be deferred to a
   follow-up plan — see open question Q5.

Commits 1–5 are the v0.1 closing definition. Commit 6 may slip.

## 14. Risks + open questions for the user

Numbered for reference back from §3, §5, §6, §7, §8, §13:

- **Q1 — UI tech choice.** CLI + JSON API only for v0.1, or also embed
  a SPA? Recommend CLI-only; SPA in a follow-up plan. (§8)
- **Q2 — Duplicate-tuple precedence.** When two LAN brokers advertise
  the same `(capability_id, offering_id)` with **different** prices,
  what wins? Reject ambiguous candidate (loud); first-wins by broker
  name (silent, deterministic); or emit both tuples (load-balance,
  matches "host is not a registration unit"). Recommend (3) but
  surface a roster-UX warning. (§5)
- **Q3 — Candidate packaging.** Tarball (`manifest.json` +
  `metadata.json`) or single-file JSON-with-sidecar? Recommend
  tarball — keeps signed bytes cleanly separated from operator-only
  metadata. (§6)
- **Q4 — Resolver-facing URL stability.** Same `/.well-known/livepeer-registry.json`
  path the prior `service-registry-daemon` resolver expects, or a new
  path? Recommend same path; existing resolvers re-target with no
  consumer-side change. (§7)
- **Q5 — Operator UI required for v0.1?** Or can a JSON-API + CLI
  first cut ship and leave UI to a follow-up? Recommend ship without
  UI; it's the most reasonable cut line. (§8, §13)
- **Q6 — Signed-manifest upload channel.** HTTP POST and filesystem
  drop both, or pick one? Recommend both; they share the same
  verify+publish pipeline so the cost is small. (§7)
- **Q7 — Where does the coordinator learn its broker hosts?** Static
  YAML for v0.1 (recommend); service discovery (Consul / DNS-SD /
  Kubernetes Service) is a follow-up. Confirm OK to defer? (§5)

## 15. Out of scope

These belong to follow-up plans, not plan 0018:

- **Automated rotation / push-style sign cycle.** R11 explicitly keeps
  hand-carry; revisit in v2 (core-beliefs.md line 35).
- **Multi-operator coordinator federation.** One orch operator per
  coordinator. Cross-orch aggregation is a third-party indexer
  concern (R10).
- **On-chain manifest pointers.** `protocol-daemon` writes
  `ServiceRegistry.setServiceURI`; the coordinator does not
  (`livepeer-modules-project/service-registry-daemon/README.md`
  lines 78–80).
- **Capability authoring UI / `host-config.yaml` editor.** Operator
  edits on the broker host; v0.1 coordinator is read-only. (§8)
- **Service discovery for brokers.** Static config in v0.1. (§5)
- **Capacity advertisements.** Killed by core belief #8 / R8;
  saturation is `503 + Livepeer-Backoff`, not a manifest field.
- **Warm-key signing on the coordinator host.** Cold key on
  secure-orch is the only signer (R11, R6).
