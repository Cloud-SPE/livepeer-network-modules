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
| `SelectMany` | Same filter | Ordered matching routes; no matches returns `not_found` |
| `ListKnown` | Empty request | Cached candidate addresses, mode and cached timestamp |
| `Refresh` | `eth_address` or `*`, `force` | Re-resolve; `force=true` bypasses TTL |
| `GetAuditLog` | address, optional since/limit | Stored audit records |
| `Health` | Empty | Process diagnostic values; see limits below |

Resolve returns the address, resolved URI, mode, nodes, freshness, timestamps
and schema version. Success can contain zero nodes, for example after policy
filtering. Capability and offering IDs are opaque. Do not normalize slash and
colon forms into an alias. Selection uses case-insensitive string matching
without semantic rewriting; use the keys returned by discovery.

`SelectMany` skips individual addresses that fail resolution. It then applies
conjunctive enabled/tier/min-weight and capability/offering filtering, then sorts by descending weight. Equal
weights preserve input order. `Select` uses that same process and returns the
first result. Separate calls need not produce identical results if health,
publications or configuration change between them.

With the production live-health provider enabled, targeted selection requires
a fresh `ready` capability/offering entry from the broker's `/registry/health`.
Unknown additive health-response fields are tolerated. Inventory resolution
is more permissive when health fetching is unavailable or empty; a visible
inventory tuple is not necessarily selectable. Static-only resolution bypasses
live-health filtering on its initial synthesis; consumer admission checks still
apply. See [backend health](../../../docs/design-docs/backend-health.md).

## Selected route fields

Routes carry `worker_url`, `eth_address`, capability, offering, `protocol`,
`work_unit`, decimal price string, `units_per_price`, optional estimator,
`extra_json`, `constraints_json`, quote metadata and `settlement_keys`.
The route's `eth_address` is the orchestrator identity, not a runner identity.

- `units_per_price` comes from signed `per_units` (zero/absent normalizes to 1).
  Do not assume prices have been divided to a per-single-unit value.
- `quote_version` is the publication sequence for manifest-derived routes,
  otherwise zero. It is not evidence of resolver replay protection.
- `quote_id` is `resolver:v1:` plus SHA-256 of lower-case eth address, worker
  URL, capability, offering and work unit, joined by `|`.
- `constraint_fingerprint` hashes the canonical JSON constraints object;
  absent/empty constraints become `{}`.
- `route_fingerprint` hashes NUL-separated lower-case eth address, worker URL,
  capability, **protocol**, offering, decimal price, work unit, units-per-price,
  canonical extra JSON and canonical constraints JSON. It does not include
  publication sequence, settlement keys or the typed estimator.
- `settlement_keys` includes all keys projected from the signed publication,
  sorted newest-first by not-before. Windows are passed through, not filtered
  against current time. Consumers verify the settlement record's issued-at
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

ListKnown does not resolve or probe entries. Overlay-only restricts it to
configured candidates that already have cache records. Missing startup
publications are retried by Select/SelectMany and Refresh, but do not appear in
ListKnown until cached. KnownEntry's wire `freshness_status` is currently left
UNSPECIFIED. Domain static-overlay mode also maps to wire UNSPECIFIED; use
node source STATIC_OVERLAY to recognize pins.

Wildcard Refresh uses all candidate addresses and swallows individual resolve
errors; use an address-specific call and audit/logs for error details. It
refreshes known/configured addresses, not the entire chain pool.

Health currently sets `chain_ok` and `manifest_fetcher_ok` to true and
`last_chain_success` to the current time. Only mode and cache size are actual
state. Standard gRPC health and metrics `/healthz` are liveness signals. None
prove chain availability, successful manifest discovery or selectable routes.
Real provider outcomes are available through metrics/logs (`lnm-cuh`).

## Publisher

Only `GetIdentity(Empty)` and `Health(Empty)` remain.
GetIdentity returns the loaded keystore address. Publisher does not build,
sign, publish or host manifests and does not write chain pointers.

## Errors

Errors carry `registry_error_code` in a `google.protobuf.Struct` status detail.

| Stable detail | gRPC status | Meaning |
|---|---|---|
| `not_found` | NotFound | Missing/disabled discovery entry, missing chain pointer, or no selectable routes |
| `manifest_unavailable` | Unavailable | HTTP/transport retrieval failed without usable fallback |
| `signature_mismatch` | Unauthenticated | Claimed/recovered signer differs from expected address |
| `parse_error` | InvalidArgument | Malformed manifest, address or request, including malformed signatures |
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
