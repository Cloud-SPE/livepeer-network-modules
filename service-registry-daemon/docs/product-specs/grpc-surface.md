# gRPC consumer contract

The authoritative wire definitions are in the sibling
[proto-contracts registry package](../../../proto-contracts/livepeer/registry/v1/resolver.proto).
They use package `livepeer.registry.v1`; the current mounted service depends
on `--mode`. All methods are unary. Earlier build/sign/probe publisher RPCs
were removed in the coordinated manifest migration; the package name alone
is not a promise of compatibility with those earlier binaries.

## Resolver

| Method | Inputs | Behavior |
|---|---|---|
| `ResolveByAddress` | `eth_address`, `allow_legacy_fallback`, `allow_unsigned`, `force_refresh` | Source-aware resolution, signature validation, overlay merge and inventory health pruning |
| `Select` | required `capability`, `offering`; optional `tier`, `min_weight` | First ranked route |
| `SelectMany` | Same filter | Snapshot-only ordered matching routes; fresh absence is `not_found`, incomplete state is `registry_unavailable` |
| `ListKnown` | Empty request | Snapshot of all discovered candidates, including never-successful addresses, with typed discovery diagnostics |
| `ListOfferings` | Optional capability/offering/tier/min_weight; include_expired | Snapshot-only catalog with metadata, selectability, timestamps and unfiltered discovery coverage |
| `Refresh` | `eth_address` or `*`, `force` | Re-resolve; `force=true` bypasses TTL and failure cooldown, but never validation |
| `GetAuditLog` | address, optional since/limit | Stored audit records |
| `Health` | Empty | Process diagnostic values; see limits below |

Resolve returns the address, resolved URI, mode, nodes, freshness, timestamps
and schema version. Success can contain zero nodes, for example after policy
filtering. Capability and offering IDs are opaque. Do not normalize slash and
colon forms into an alias. Selection uses case-insensitive string matching
without semantic rewriting; use the keys returned by discovery.

`Select` and `SelectMany` read one immutable indexed snapshot, with no outbound
network calls or waits for background refresh. They enforce enabled/tier/min-weight,
capability/offering, source freshness, signed validity and cached live health,
then sort by descending weight. Equal weights preserve orchestrator-address order
and manifest node order. `Select` returns the first result. Separate calls may
observe different publications or health snapshots.

A valid matching route succeeds despite unrelated refresh failures. If none exists,
incomplete/cold/expired state returns `UNAVAILABLE` (`registry_unavailable`);
a sufficiently fresh view proving no eligible match returns `NOT_FOUND`.
See [snapshot and refresh semantics](../design-docs/resolver-cache.md).

With the production live-health provider enabled, targeted selection requires
a fresh `ready` capability/offering entry from the broker's `/registry/health`.
Unknown additive health-response fields are tolerated. Inventory resolution
is more permissive when health fetching is unavailable or empty; a visible
inventory tuple is not necessarily selectable. Static-only resolution bypasses
live-health filtering on its initial synthesis; consumer admission checks still
apply. See [backend health](../../../docs/design-docs/backend-health.md).

## Cached offering catalog (LOC contract)

`ListOfferings` always returns immediately from one immutable snapshot. It never
waits for refresh, fetches health, resolves an address, or enumerates the chain.
Partial and uninitialized catalogs return gRPC OK. `evaluated_at` is the time at
which time-dependent eligibility is checked; `snapshot_at` is when the underlying
snapshot was published. A catalog is informational, not a paid-work authorization.

Each `CatalogOffering.offering` is the full `SelectedRoute` projection:

- `eth_address` is the orchestrator **and payee**; `worker_url` is its advertised
  broker endpoint. `worker_id` is the resolver node ID scoped to that orchestrator.
  Internal runner identity is not advertised and is not synthesized.
- Work units, price denominator, estimator, protocol, opaque protocol axes in
  `extra_json`, constraints, quote/fingerprint and settlement fields are relayed
  through the same projection used by SelectMany.
- Prices are informational snapshot data. LOC must call Select/SelectMany and
  validate the returned route when authorizing paid work.
- `selectable` uses the same eligibility predicate as SelectMany at `evaluated_at`:
  source/manifest TTL, signed validity, signature and overlay policy, settlement
  key windows, fresh ready cached health, tier and minimum weight. It does not
  guarantee later broker admission. Inventory can be visible while nonselectable.
- `valid_until` is the earliest metadata/source/publication deadline;
  `health_valid_until` is independent. `include_expired=false` omits expired
  metadata. `include_expired=true` returns it for diagnostics with `expired=true`,
  `selectable=false` and exclusion reason `metadata_expired`. Rejected publications
  are never returned, even with this flag. Static/CSV inventory is included only
  under the same configured signature policy as selection; its verified_at is absent.

Optional capability/offering filters use exact case-insensitive matching.
Tier/min_weight affect `selectable`, rather than removing inventory. No workload
names or protocols are enumerated by the registry.

`discovery_scope` is `chain_active_pool_and_overlay` (the last discovered active
chain pool plus enabled overlay discovery entries), or `overlay_only` (enabled
configured manifest URLs/static pins). This is not a claim to enumerate the entire
permissionless network. Chain scope observations expire 24 hours after the last
successful enumeration and refresh on round discovery. Overlay-only configuration
is authoritative until replaced/restarted, so discovery_valid_until is absent.
Failures of scope enumeration make discovery_scope_authoritative false.

`COMPLETE` requires authoritative fresh scope, fresh positive or conclusive
negative evidence for every address, and nonexpired health/settlement evidence
for known inventory at evaluated_at. Known non-ready health is a conclusive
negative. `PARTIAL` indicates missing, expired or indeterminate evidence;
`UNINITIALIZED` indicates no completed source enumeration. Counts always cover
**the unfiltered scope**, regardless of request filters and include_expired.
Compatibility counts partition known_addresses; expired/deferred/unavailable
counts overlap. They describe evidence, not selectable-provider totals.
A cooldown alone neither invalidates fresh evidence nor extends its deadline.
`coverage_valid_until` gives the earliest known address/health deadline; it can
be absent when there is no evidence, and is not proof of completeness by itself.

An empty COMPLETE response is authoritative absence only within these scope,
time and filter bounds. Empty PARTIAL/UNINITIALIZED is unavailable discovery.
See [protobuf JSON fixtures](../../../proto-contracts/livepeer/registry/v1/testdata/README.md)
for populated partial, empty partial, empty complete, uninitialized, expired
inventory and deferred-resolution examples.

## Selected route fields

Routes carry `worker_url`, `eth_address`, capability, offering, `protocol`,
`work_unit`, decimal price string, `units_per_price`, optional estimator,
`extra_json`, `constraints_json`, quote metadata, `settlement_keys` and
`settlement_domain_id`. The route's `eth_address` is the orchestrator
identity, not a runner identity.

- `units_per_price` comes from signed `per_units` (zero/absent normalizes to 1).
  Do not assume prices have been divided to a per-single-unit value.
- `quote_version` is the publication sequence for manifest-derived routes,
  otherwise zero. The resolver rejects rollback and conflicting same-sequence signed payloads.
- `quote_id` is `resolver:v1:` plus SHA-256 of lower-case eth address, worker
  URL, settlement-domain ID, capability, offering and work unit, joined by `|`.
- `constraint_fingerprint` hashes the canonical JSON constraints object;
  absent/empty constraints become `{}`.
- `route_fingerprint` hashes NUL-separated lower-case eth address, worker URL,
  settlement-domain ID, capability, **protocol**, offering, decimal price, work unit, units-per-price,
  canonical extra JSON and canonical constraints JSON. It does not include
  publication sequence, settlement keys or the typed estimator.
- `settlement_keys` includes all keys projected from the signed publication,
  sorted newest-first by not-before. All windows are passed through. If keys are advertised, at least one must be
  valid at selection time; expired siblings remain in the response for historical verification. Consumers verify the settlement record's issued-at
  against the key window. `introduced_in_publication_seq` currently carries
  the containing publication's sequence, not independently tracked key history.
- The optional `work_unit_estimator` is typed; consumers must implement the
  advertised estimator when their protocol requires it.

The inventory `Node` wire projection is narrower than `SelectedRoute`: its
capabilities do not have a typed protocol field, offerings lack the denominator,
and nodes lack settlement keys. Use Select/SelectMany for the complete route
contract. Declared protocol/axes are also mirrored into capability extra JSON.

Static pin prices and protocol/work-unit completeness are not fully validated
by selection. Signed tuples receive structural checks at decode; consumers
must still validate their payment and protocol prerequisites.

## Signature policy

Verified manifests are always allowed. An unsigned or invalid coordinator
envelope is rejected regardless of unsigned flags. CSV and static pins are
unsigned nodes, permitted by per-address `unsigned_allowed`, an explicit
Resolve request allowance, or daemon `--reject-unsigned=false`. Select and
SelectMany apply daemon/overlay policy; they do not implicitly allow unsigned
nodes. Legacy nodes have a distinct legacy status and no signed capability
inventory.

## Diagnostics and refresh limits

ListKnown reads the same in-memory discovery snapshot as the catalog. It never
resolves, probes or reads the database. It includes active/configured candidates
without accepted publications. Its new `discovery_status` is authoritative for
compatibility, last manifest availability, typed failure reason, retry policy,
consecutive failures, last successful verification and next retry time. The old
`freshness_status` remains UNSPECIFIED; catalog entries provide explicit time
bounds. Static-overlay mode still maps to wire UNSPECIFIED.

Ordinary Resolve returns a usable cache entry or `resolution_deferred` during
failure backoff, with zero chain, manifest or broker-health I/O on that path.
Outside cooldown it may perform a bounded refresh. Cache reads do not renew
verification timestamps or reset failure counters. Explicit force bypasses the
schedule only. Concurrent refresh requests share one address attempt; canceling
one waiter does not cancel an attempt still needed by another. The last departing
waiter cancels and joins the operation. All-location retrieval/verification fits
within a five-second total refresh deadline.

An attempted transport failure may return diagnostic last-good inventory within
MaxStale and signed validity, as before; that result never renews selection TTLs.
During subsequent cooldown reads, cached inventory must satisfy normal freshness
bounds. Rejected signatures, invalid publications and changed sources cannot use
last-good fallback, including after restart. Legacy fallback is diagnostic only
and does not establish incompatibility or reset the manifest retry state.

Wildcard Refresh uses all candidate addresses and swallows individual resolve
errors; use an address-specific call and audit/logs for error details. It
refreshes known/configured addresses, not the entire chain pool.

Health reports the most recently completed serviceURI read and manifest HTTP
fetch. Before the first attempt a required provider is false. Unused providers
are true (not required), so overlay-only has chain_ok=true and no chain-success
timestamp; publisher needs neither provider. NotFound from a chain read is a
successful RPC. Failures preserve the last actual successful chain timestamp.
These observations do not actively probe or imply a valid signature or a
selectable route. Standard gRPC health and metrics /healthz are liveness signals.

## Publisher

Only `GetIdentity(Empty)` and `Health(Empty)` remain.
GetIdentity returns the loaded keystore address. Publisher does not build,
sign, publish or host manifests and does not write chain pointers.

## Errors

Errors retain `registry_error_code` in a `google.protobuf.Struct` status detail.
Attempted discovery failures and deferred responses additionally carry the
canonical `RegistryResolutionDetail` protobuf (`eth_address`, `discovery_status`,
`evaluated_at`). When a retry is scheduled in the future they also carry standard
`google.rpc.RetryInfo.retry_delay`, measured at evaluation. Deferred resolution
has gRPC UNAVAILABLE and stable code `resolution_deferred`. Consumers must not
parse error messages. See the canonical proto and fixtures linked above.

`DiscoveryStatus.compatibility` is UNKNOWN, VERIFIED_COMPATIBLE or
CONFIRMED_INCOMPATIBLE for the current source. `availability` records the last
manifest attempt, not worker readiness. `failure_reason` records the current
failure; all three remain independent of route eligibility. Timeouts never
establish incompatibility. Incompatibility requires conclusive missing/unsupported
results across all supported candidate locations. Malformed publications,
signature failures, expiry and rollback have separate failure reasons. The
compatibility evidence timestamps bound negative absence claims independently
of next_retry_at. Successful verification resets counters; reads never do.

| Stable detail | gRPC status | Meaning |
|---|---|---|
| `resolution_deferred` | Unavailable | No usable cache and next scheduled retry is in the future; no provider calls were made |
| `manifest_unsupported` | FailedPrecondition | Conclusive evidence of an unsupported manifest shape |
| `canceled` | Canceled | Caller canceled the operation |
| `deadline_exceeded` | DeadlineExceeded | Caller/operation deadline elapsed |
| `registry_unavailable` | Unavailable | Selection has no eligible match and incomplete, expired or unverifiable state prevents a reliable absence answer |
| `not_found` | NotFound | Missing/disabled discovery entry, missing chain pointer, or no selectable routes |
| `manifest_unavailable` | Unavailable | HTTP/transport retrieval failed without usable fallback |
| `signature_mismatch` | Unauthenticated | Claimed/recovered signer differs from expected address |
| `parse_error` | InvalidArgument | Malformed manifest, address or request, including malformed signatures, invalid publication windows, and replay/conflicting sequences |
| `unknown_field` | InvalidArgument | Unknown envelope field where classified separately |
| `manifest_too_large` | ResourceExhausted | Body exceeds fetch size cap |
| `chain_unavailable` | Unavailable | Chain lookup failed without usable last-good |
| `unknown_mode` | InvalidArgument | Chain pointer cannot be classified |
| `cache_stale_failing` | DeadlineExceeded | Reserved mapping; current max-stale failure returns its underlying error |
| `keystore_locked` | FailedPrecondition | Keystore unavailable |
| `chain_write_failed` | FailedPrecondition | Retained mapping for removed write paths; no current publisher write RPC |
| `internal` | Internal | Unclassified internal failure |

There is no daemon-level application authentication on the unix socket.
Control local access through host/container socket permissions. Calling a
service not mounted in the selected mode returns gRPC Unimplemented.
