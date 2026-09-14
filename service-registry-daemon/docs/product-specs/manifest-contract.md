# Manifest contract

## Authoritative format and publication

The [protocol manifest](../../../livepeer-network-protocol/manifest/README.md)
and [schema](../../../livepeer-network-protocol/manifest/schema.json) define the
signed envelope: outer `manifest` payload plus `signature`. The payload carries
`spec_version`, `publication_seq`, `issued_at`, `expires_at`, `orch`, capability
tuples and optional `settlement_keys`. The old flat v3.0.1 node-shaped manifest
is rejected. Resolver nodes are an internal/wire projection, not a second
publication format.

The coordinator builds candidates from broker inventory. The cold console
signs through the operator-driven flow; the coordinator hosts the returned
signed document. Signature algorithm is `secp256k1` over the personal-sign hash
of JCS-canonical **entire payload**, including issued-at and expiry. The exact
scheme is specified by the protocol document.

A manifest pointer comes from chain `serviceURI` or YAML `manifest_url`.
Use the coordinator's HTTPS `/.well-known/livepeer-registry.json` endpoint.
The resolver also supports documented base-URL probing for chain compatibility.
It does not use HTTP Cache-Control or ETag for freshness; configured TTL governs
its own cache. Fetch size defaults to 4 MiB, configurable up to 16 MiB.

## Implemented verification and projection

`types.DecodeCoordinatorEnvelope` strictly decodes known envelope fields and
validates the structural requirements in `coordinator_envelope.go`. This is a
Go boundary validator, not execution of every rule in the JSON Schema.
`CoordinatorCanonicalBytes` canonicalizes the payload; the resolver recovers
the signer and requires both claimed and recovered identity to match the
expected orchestrator address.

Capability tuples are grouped by `worker_url` into synthetic `node-N` IDs.
Each tuple carries capability and offering, protocol/axes, work-unit metadata,
price numerator and `per_units` denominator, settlement-domain identity, optional
extra and constraints. Protocol-major-4 tuples require a canonical nonzero
`settlement_domain_id`; the decoder rejects different domains for the same worker
URL. Earlier manifests can still be decoded for diagnosis but cannot supply the
required domain for the major-4 paid path.
Protocol and declared axes pass through; workload validation belongs to the
consumer. Reserved declaration keys may not be shadowed by `extra`.

Use Select/SelectMany for the full route projection, including typed protocol,
price denominator, estimator, settlement keys and `settlement_domain_id` (field 17).
The ID is included in quote/route fingerprints so a new financial domain cannot
reuse the old route identity. The inventory Node proto is
narrower. See [gRPC contract](grpc-surface.md).

An unsigned allowance applies to static/CSV node sources, never to unsigned or
invalid coordinator envelopes. Static YAML cannot delegate settlement keys.
Signed claims authenticate their author; they do not prove present capacity,
that a broker honors its advertised price, or valid ticket funding.

## Publication validity and replay protection

After signature recovery, the resolver requires `issued_at <= now < expires_at`
and a strictly positive validity window. Cache hits and last-good fallbacks
recheck that window; TTL cannot extend a signed publication's lifetime. Old
cache records without expiry are refetched. Keep resolver clocks synchronized.

For each orchestrator address the store retains the highest accepted
`publication_seq` and hash of the canonical signed payload. Lower sequences and
conflicting payloads at the same sequence are rejected. Identical payloads can
be refetched despite whitespace or signature encoding changes. The watermark
survives coordinator URL changes, cache deletion and restart. Signed routes are
not returned if durable acceptance fails. Deleting the database resets this
history; back up and preserve it across upgrades.

On upgrade, an old cache without a canonical hash accepts the same sequence
only if its raw document matches; a higher sequence can establish a new hash.
Watermark and cache writes are separate: a crash after watermark persistence
can require a refetch, but does not permit an older cached publication.

All advertised settlement keys retain their signed validity windows, including
historical keys. Consumers check record-time validity. `spec_version` presence
is checked; this daemon does not negotiate supported major versions or evaluate
every protocol-specific axis. Publication validity/replay failures are returned
as `parse_error` details (InvalidArgument); Select skips rejected candidates.

## Moving a coordinator

Publish the valid signed document at the new URL first. Update the chain
pointer using the protocol-daemon flow, or update YAML and restart the resolver.
Chain pointer changes are discovered by stale/forced resolution or round
seeding. Overlay pointer changes invalidate reuse of the old source on the
next resolution after restart. An unchanged coordinator may publish multiple
broker URLs under one orchestrator identity; multiple identities require
separate manifests.

## Settlement-domain rollout

The payment daemon owns the immutable ledger ID. Broker inventory carries it into
each cold-signed tuple; the registry relays it and does not generate or infer it
from the payee, broker URL or static overlay. Independent brokers may share a
payee while exposing different IDs, balances and account versions. See the
[identity and migration contract](../../../docs/design-docs/settlement-domain-identity.md).
