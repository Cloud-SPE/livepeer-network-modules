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
price numerator and `per_units` denominator, optional extra and constraints.
Protocol and declared axes pass through; workload validation belongs to the
consumer. Reserved declaration keys may not be shadowed by `extra`.

Use Select/SelectMany for the full route projection, including typed protocol,
price denominator, estimator and settlement keys. The inventory Node proto is
narrower. See [gRPC contract](grpc-surface.md).

An unsigned allowance applies to static/CSV node sources, never to unsigned or
invalid coordinator envelopes. Static YAML cannot delegate settlement keys.
Signed claims authenticate their author; they do not prove present capacity,
that a broker honors its advertised price, or valid ticket funding.

## Current implementation limits

Protocol requirements and daemon enforcement must not be conflated:

- Issued-at/expiry must be present, but fetch/cache resolution does not enforce
  the publication's current validity window.
- Publication sequence is propagated and cached; lower-sequence or conflicting
  same-sequence publications are not rejected by a monotonic replay guard.
- All signed settlement keys are forwarded, with their validity windows, rather
  than filtered against current time. Consumers must check record-time validity.
- `spec_version` presence is checked; this daemon does not negotiate supported
  major versions or evaluate every protocol-specific axis.

Expiry/replay enforcement is tracked in `lnm-dnh`. Do not interpret cache
freshness or `quote_version` as proof that those missing checks ran.

## Moving a coordinator

Publish the valid signed document at the new URL first. Update the chain
pointer using the protocol-daemon flow, or update YAML and restart the resolver.
Chain pointer changes are discovered by stale/forced resolution or round
seeding. Overlay pointer changes invalidate reuse of the old source on the
next resolution after restart. An unchanged coordinator may publish multiple
broker URLs under one orchestrator identity; multiple identities require
separate manifests.
