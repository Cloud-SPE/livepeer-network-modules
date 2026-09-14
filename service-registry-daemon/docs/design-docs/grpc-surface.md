---
title: gRPC surface
status: verified
last-reviewed: 2026-09-14
---

# gRPC surface

The [consumer contract](../product-specs/grpc-surface.md) is the detailed method
and field reference. The wire definitions live in
[proto-contracts](../../../proto-contracts/livepeer/registry/v1/resolver.proto).

A resolver daemon mounts Resolver; an identity-only publisher mounts Publisher.
Both mount standard gRPC health and reflection. The listener is a unix socket,
with panic recovery, deadline and structured logging interceptors. There is no
TCP gRPC listener or application-level authentication. All calls are unary.

Resolver exposes ResolveByAddress, Select, SelectMany, ListKnown, Refresh,
GetAuditLog and Health. Publisher exposes GetIdentity and Health only. Manifest
building and signing are outside this daemon; removed publisher methods must
not be advertised as reserved working endpoints.

The Go-native server owns selection/projection; adapters convert it to proto
messages. This distinction matters: domain types carry more information than
inventory Node messages. SelectedRoute is the gateway-facing contract for
protocol, denominator, estimator, quote metadata and settlement keys.

Eth addresses and integer wei prices are strings. A route price is a numerator
plus `units_per_price`, not necessarily a normalized one-unit price. Consumers
match opaque capability/offering identifiers and implement the selected
protocol. There is no registry-side payment authorization.

Provider diagnostics in Health are placeholders today. ListKnown freshness is
not populated, wildcard Refresh swallows per-address errors, and static-overlay
domain mode maps to wire UNSPECIFIED. These are documented implementation
limits, not readiness or consistency guarantees.
