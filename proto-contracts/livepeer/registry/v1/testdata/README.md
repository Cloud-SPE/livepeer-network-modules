# LOC catalog and deferred-resolution fixtures

These are protobuf JSON fixtures, decoded by `catalog_fixtures_test.go` against
canonical generated messages. Decode catalog files as `ListOfferingsResult` and
`resolution-deferred.json` as `google.rpc.Status`. Byte fields use protobuf JSON
base64. IDs and URLs are illustrative.

- `catalog-populated-partial.json`: one usable provider plus unresolved discovery.
- `catalog-empty-partial.json`: no inventory, discovery unresolved.
- `catalog-empty-complete.json`: current conclusive negative evidence; no offerings.
- `catalog-expired-entries.json`: diagnostic response to `include_expired=true`;
  expired metadata remains nonselectable even if the published snapshot is older.
- `catalog-uninitialized.json`: source discovery has not completed.
- `resolution-deferred.json`: UNAVAILABLE, stable Struct code, typed discovery
  detail and a standard RetryInfo duration.

`offering.eth_address` is the orchestrator and payee. `worker_url` is the
advertised broker endpoint; `worker_id` is the resolver node ID scoped to that
orchestrator. Internal runners are neither disclosed nor synthesized.
`offering` relays the full SelectedRoute metadata, including work units,
denominator, estimator, opaque protocol axes in extra_json, constraints and
settlement metadata. Pricing is informational; select and validate again when
authorizing paid work.

Coverage counts always describe the entire configured discovery scope, before
capability/offering/tier/min_weight filters. Compatibility counts partition the
scope; expired, deferred and unavailable counts overlap. Completeness includes
source freshness, address evidence and current health evidence. A negative
observation expires independently of its next scheduled retry. COMPLETE means
all evidence is sufficiently fresh for the stated scope at evaluated_at, not
that the entire permissionless network has been exhaustively discovered.
