# Plan 0018 — orch-coordinator design

> Historical design note: the coordinator is now implemented. Where this plan
> says "optional" or describes pre-implementation choices, prefer the shipped
> behavior in [`../../design-docs/backend-health.md`](../../design-docs/backend-health.md),
> [`../../design-docs/architecture-overview.md`](../../design-docs/architecture-overview.md),
> and the `orch-coordinator/` code.

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
- `/registry/health` for the live-availability signal. The shipped
  coordinator polls both `/registry/offerings` and `/registry/health`,
  keeps them separate, and does not mutate signed-manifest content from
  live-health state.
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
`--scrape-interval`. In the shipped design, the coordinator caches
offerings and live health separately and surfaces freshness for both in
the roster view.

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

**Aggregation rules — deduplication.** **Locked (Q2, §13).** The
uniqueness key for tuple identity is the canonicalized
`(capability_id, offering_id, extra, constraints)` quadruple.
`worker_url` is **not** part of the uniqueness key — it is the
endpoint, not an identity. Hardware-tier or fleet-perf differentiation
that an operator wants the gateway to select on goes in the `extra` /
`constraints` fields of the tuple, never in the URL.

Three cases the coordinator must handle when scraping multiple
brokers:

1. **Identical key, different prices** — operator error. Coordinator
   **hard-fails loudly**: refuses to build the candidate and surfaces
   a structured roster error citing both broker sources and the
   conflicting prices. No silent precedence rule. Two brokers
   advertising the same identity at different prices is a
   misconfiguration the cold key must not paper over.
2. **Identical key, identical price, different `worker_url`** —
   legitimate HA pair. Coordinator emits a **single tuple** whose
   `worker_url` is one of the two endpoints (chosen by lexicographic
   order over `worker_url` for determinism); the second endpoint is
   recorded in the metadata sidecar for operator visibility but does
   not enter the signed bytes. (Rule documented consistently across
   roster + audit; no per-call coin-flip.)
3. **Different `extra` or `constraints`** — distinct identities. Both
   are emitted as separate tuples; the gateway uses those fields for
   selection.

Fits "host is not a registration unit" (architecture-overview.md line
127): a tuple is a capability binding, not a host record. See §13 Q2
lock for the resolution context.

## 6. Candidate-build pipeline

What the coordinator hands the secure-orch operator.

**Binary format. Locked (Q3, §13): tarball** with two members:

- `manifest.json` — JCS-canonicalized manifest payload (no signature
  yet). Byte-identical to what `secure-orch` will sign. This is the
  load-bearing constraint: the cold key signs whatever the coordinator
  built, byte-for-byte, so the diff the operator sees in
  `secure-orch-console` is a diff of the same bytes that hash and
  sign.
- `metadata.json` — operator-only sidecar (NOT signed) with: candidate
  timestamp, scrape window (start+end), source broker URLs and per-
  broker scrape-success status, coordinator git commit, schema version.

The split is load-bearing: the signed file is what the cold key
endorses; the sidecar is operator-useful provenance that must not
enter the signed bytes.

**Diffing surface.** Two diffs run in the publication cycle, on the
two sides of the air gap:

- **Coordinator UI diff** — candidate vs **currently-published**
  manifest. Shown to the operator on the LAN laptop **before** the
  hand-carry trip. Purpose: short-circuit no-op trips (operator
  updates `host-config.yaml`, restarts broker, nothing materially
  changed → operator sees "no diff" and skips the cold-key trip).
  Advisory UX hint, not authoritative.
- **Secure-orch-console diff** — candidate vs **last-signed** manifest
  (the bytes the cold key most recently endorsed). Authoritative diff
  for signing. Lives in the `secure-orch-console` plan; mentioned here
  only because the coordinator-side diff is its precursor.

The two diffs can disagree briefly (e.g. an old signed manifest is
still live but a newer one was signed and is in flight). Both surfaces
remain useful; the secure-orch one is what the cold key acts on.

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

**Upload surface.** **Single channel (Q6, §13): HTTP POST
`/admin/signed-manifest`** (multipart upload, mTLS or shared secret),
driven by the coordinator's web UI upload form. One verify+publish
path; no secondary surface to harden, no inotify watcher to maintain.
Operators with SCP / USB workflows `scp` the signed file to their LAN
laptop and click upload in the web UI from there.

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

**Resolver-facing endpoint.** **Locked (Q4, §13): same
`GET /.well-known/livepeer-registry.json` path** the prior
`service-registry-daemon` resolver expects
(`livepeer-network-protocol/manifest/README.md` lines 4 + 34).
Existing resolvers re-target with zero consumer-side change.

> **Two on-chain registries.** Livepeer mainnet has both
> `ServiceRegistry` (transcoding, legacy) and `AIServiceRegistry`
> (AI workers, newer). Each is a different contract address on
> Arbitrum One. An orch may be registered in either or both. The
> rewrite **consolidates** to one well-known URL and one unified
> manifest format whose `capabilities[]` list mixes transcoding and
> AI tuples. The resolver/gateway side is configured with which
> contract address(es) to query for the orch's `serviceURI`; the orch
> may register the same URL in both contracts. See §13 commit #0 for
> the doc-cleanup that bakes this consolidation into
> `livepeer-network-protocol/manifest/README.md` and
> `docs/design-docs/architecture-overview.md` before any coordinator
> code reads on-chain pointers.

## 8. UX surface — capability-as-roster-entry

The operator-facing UI. The roadmap row's outcome statement
(`PLANS.md` line 108) names this work.

**Tech choice. Locked (Q1, §13): web UI is the primary surface for
v0.1.** The coordinator runs on the operator's LAN — the operator-UI
listener binds the LAN interface (`--listen=:8080`) and the operator
hits it from a browser on the same LAN. No `ssh -L` tunnel needed
(unlike `secure-orch`, the coordinator is LAN-reachable by design;
that's the whole point of the LAN-side build/host split). The web UI
is served by the coordinator's own Go HTTP server with HTML/CSS/JS
embedded via `embed.FS`, mirroring the pattern locked in plan 0019
§6.1. The JSON HTTP API the web UI calls is the same surface a
scripted operator could use directly. **CLI is deferred or never** —
it doesn't add value over `curl` against the JSON API and would be a
second surface to maintain.

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

**Row identity.** Roster row identity uses the §5 / Q2 uniqueness key
`(capability_id, offering_id, extra, constraints)`. Drift detection
groups rows by that key (not by `worker_url`); a tuple whose only
change is a swapped HA endpoint shows as "no drift" rather than
"removed + added."

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
| `--listen=:8080` | operator UX HTTP API + embedded web UI (LAN-bound) |
| `--public-listen=:8081` | resolver-facing `/.well-known/livepeer-registry.json` only |
| `--metrics-listen=:9091` | Prometheus |
| `--data-dir=/var/lib/livepeer/orch-coordinator` | history + audit + currently-published manifest |
| `--log-level={debug,info,warn,error}` | slog |
| `--log-format={text,json}` | slog |
| `--scrape-interval=30s` | scrape cadence |
| `--scrape-timeout=5s` | per-broker timeout |
| `--freshness-window=150s` | drop-stale-tuples threshold |
| `--manifest-ttl=24h` | `expires_at = issued_at + ttl` |
| `--version` | print version and exit |

`--public-listen` exposes **only** `GET /.well-known/livepeer-registry.json`;
all other paths return 404. Defense-in-depth: a routing bug elsewhere
in the codebase cannot accidentally expose admin or operator-UX routes
via the public listener.

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
        web/                          # embedded HTML/CSS/JS via embed.FS
      publicapi/                      # resolver-facing /.well-known/... (only)
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

Seven commits, each independently shippable as a milestone:

0. **Manifest spec doc cleanup (two-registry consolidation).**
   Pure-doc commit, no code. Update
   `livepeer-network-protocol/manifest/README.md` to explicitly note
   that this manifest format covers **both** transcoding capabilities
   (`ServiceRegistry`) and AI capabilities (`AIServiceRegistry`) in
   one unified `capabilities[]` list. Update
   `docs/design-docs/architecture-overview.md` to clarify that an orch
   may register the same well-known URL in either or both contracts
   (different addresses on Arbitrum One) and the resolver/gateway side
   is configured with which contract address(es) to query. **Lands
   first** so the rest of the coordinator code reads on-chain pointers
   against documented two-registry semantics rather than backfilling
   the explanation later.
1. **Scaffold + scrape only.** Component skeleton, config parser,
   broker HTTP client, scrape loop, in-memory candidate cache.
   `--dev` mode boots and scrapes a fake broker. **No candidate output
   yet.** Lands the directory layout (§12) and the config surface
   (§11) early so other plans can pin against them.
2. **Candidate build.** JCS canonicalization, idempotent build,
   filesystem snapshot tarball (`manifest.json` + `metadata.json`),
   history pruning. JSON HTTP endpoint to dump the current candidate.
3. **Diff surface + roster materialization.** Candidate-vs-published
   diff computation (the coordinator-UI advisory diff per §6),
   roster view assembly using the §5 / Q2 uniqueness key. Still no
   inbound from secure-orch.
4. **Signed-manifest hosting.** Receive-and-verify pipeline —
   **HTTP POST `/admin/signed-manifest` only**. No inbox / inotify /
   filesystem-watcher code path. Signature recovery, schema-drift
   check, atomic swap to live, audit log.
5. **Resolver-facing endpoint + Prometheus.** `/.well-known/livepeer-registry.json`
   served from the live store on `--public-listen` (404 elsewhere on
   that listener), full metrics surface, structured logging, runbook
   draft.
6. **Web UI.** Embedded HTML/CSS/JS via `embed.FS`, served by the same
   Go HTTP server backing the JSON API on `--listen`. Roster view,
   diff view, signed-manifest upload form. **Required for v0.1**
   (Q1 + Q5 lock — web UI is the primary operator surface).

Commits 0–6 are the v0.1 closing definition.

**Implementation flags carried by the commits above:**

- The two-diff pattern (coordinator-UI advisory diff vs
  secure-orch-console authoritative diff) is realized in commit 3 on
  this side; the secure-orch counterpart lives in plan 0019.
- Roster row identity uses the §5 / Q2 uniqueness key in commit 3.
- The `--public-listen` lockdown (§11) is enforced in commit 5: only
  `/.well-known/livepeer-registry.json` is routed; everything else 404.

## 14. Resolved decisions

All seven open questions were resolved on 2026-05-06 in a user
walk-through. Numbered for reference back from §3, §5, §6, §7, §8,
§13.

- **Q1 — UI tech.** **DECIDED: web UI is the primary operator
  surface for v0.1.** The coordinator runs on the operator's LAN, so
  the operator-UI listener binds the LAN interface and the operator
  hits it from a browser on the same LAN — no `ssh -L` tunnel needed
  (the LAN reachability is the whole reason the coordinator exists as
  a separate component from secure-orch). Same Go-HTTP-server +
  `embed.FS` static-asset pattern as plan 0019 §6.1. CLI is deferred
  or never; the JSON HTTP API is the scriptable surface and `curl`
  is sufficient. (§8, §13 commit 6)
- **Q2 — Duplicate-tuple precedence.** **DECIDED: uniqueness key is
  the canonicalized `(capability_id, offering_id, extra, constraints)`
  quadruple; `worker_url` is not part of identity.** Three cases.
  Identical key with different prices → operator error, hard-fail
  loudly with a roster error citing both broker sources. Identical
  key + identical price + different `worker_url` → legitimate HA
  pair, emit one tuple (lex-ordered `worker_url` for determinism;
  the second endpoint goes in the metadata sidecar). Different
  `extra` or `constraints` → distinct identities, emit both tuples;
  the gateway uses those fields for selection. (§5)
- **Q3 — Candidate packaging.** **DECIDED: tarball with
  `manifest.json` (JCS-canonical, signed bytes) plus `metadata.json`
  (operator-only sidecar — candidate timestamp, scrape window, source
  broker URLs and per-broker scrape-success status, coordinator git
  commit, schema version).** Cleanly separates signed bytes from
  operator-useful provenance. (§6)
- **Q4 — Resolver-facing URL stability.** **DECIDED: same
  `/.well-known/livepeer-registry.json` path** as the prior
  `service-registry-daemon`; existing resolvers re-target with zero
  consumer-side change. **And** the rewrite consolidates the two
  on-chain registries (`ServiceRegistry` for transcoding,
  `AIServiceRegistry` for AI workers — different contract addresses
  on Arbitrum One) onto one well-known URL with one unified
  manifest whose `capabilities[]` list mixes transcoding and AI
  tuples. The resolver/gateway side is configured with which
  contract address(es) to query; the orch may register the same URL
  in either or both. §13 commit #0 bakes this into
  `livepeer-network-protocol/manifest/README.md` and
  `docs/design-docs/architecture-overview.md` before any coordinator
  code reads on-chain pointers. (§7)
- **Q5 — Operator UI required for v0.1?** **DECIDED: yes — web UI
  ships with v0.1.** With Q1's reframing (web UI is the primary
  surface, not an alternative to a CLI), §13 commit 6 is required for
  the v0.1 close, not optional. (§8, §13)
- **Q6 — Signed-manifest upload channel.** **DECIDED: HTTP POST
  only — `/admin/signed-manifest` (multipart, mTLS or shared
  secret), driven by the web UI's upload form.** Filesystem-drop /
  inotify / spool-dir alternative is dropped entirely. Cleaner upload
  pipeline, single verify+publish path, no inotify watcher to
  maintain, no secondary admin surface to harden. Operators with SCP
  / USB workflows `scp` to the LAN laptop and click upload. (§7)
- **Q7 — Broker discovery.** **DECIDED: static YAML for v0.1.**
  Operator lists each broker by name + `base_url` in
  `coordinator-config.yaml`; coordinator polls each every
  `--scrape-interval` (default 30s). Most operators have ≤5 brokers;
  service discovery (DNS-SD / Consul / Kubernetes Service) is a
  follow-up plan. (§5)

## 15. Out of scope

These belong to follow-up plans, not plan 0018:

- **Filesystem drop / inotify / spool-dir watcher** for signed-manifest
  upload. v0.1 is HTTP POST only (Q6). Operators with SCP / USB
  workflows `scp` the file to their LAN laptop and click upload in the
  web UI.
- **CLI surface for the coordinator.** Web UI is the primary operator
  surface (Q1). CLI is deferred to a future plan if operator demand
  justifies a separate maintenance surface; the JSON HTTP API + `curl`
  covers the scriptable cases until then.
- **Service discovery for brokers.** Static YAML in v0.1 (Q7).
  DNS-SD / Consul / Kubernetes Service discovery is a future plan.
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
- **Capacity advertisements.** Killed by core belief #8 / R8;
  saturation is `503 + Livepeer-Backoff`, not a manifest field.
- **Warm-key signing on the coordinator host.** Cold key on
  secure-orch is the only signer (R11, R6).
