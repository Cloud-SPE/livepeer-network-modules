---
title: Broker offerings and runner discovery
status: verified
last-reviewed: 2026-09-14
---

# Broker offerings and runner discovery

The filename is retained for existing links. In the connected-runner
architecture, **the capability broker**, not each runner, exposes
`GET /registry/offerings`. The registry daemon does not call this endpoint.

```text
runner attach/describe → capability broker → coordinator candidate
    → operator cold signing → coordinator signed publication → resolver
```

Broker inventory is unsigned. Its body contains `spec_version`,
`orch_eth_address`, optional `offers_revision`, and flat capability tuples.
Tuples include settlement-domain ID, capability/offer ID, protocol and axes,
work-unit metadata, price numerator, per-units denominator, extra and
constraints. The broker emits constraints as an object (possibly empty). It does not supply the public
manifest `worker_url`; the coordinator assigns its configured broker base URL.
The coordinator checks compatible major spec versions when merging inventory.

An attached runner describes supported capabilities and engagement shape;
broker-authored offers determine pricing and certification. Certified runner
shape freezes the offer's signed declaration. Runner changes do not silently
rewrite a published manifest. See
[connected-runner migration](../../../docs/design-docs/migrating-to-connected-runners.md)
and [runner attach](../../../livepeer-network-protocol/protocols/runner-attach.md).

The source contracts are [broker registry endpoints](../../../capability-broker/internal/server/registry/offerings.go)
and [coordinator broker client](../../../orch-coordinator/internal/providers/brokerclient/brokerclient.go).
Do not use the old worker.yaml/fleet_workers/Zod-SPA instructions from the
previous suite; this monorepo uses the broker/coordinator Go implementation.

For resolver deployment, configure the coordinator's signed HTTPS endpoint as
`manifest_url` or publish it in chain `serviceURI`. The broker's unsigned
inventory is not an alternative signed manifest. `interaction_mode` is not a
current overlay capability field; use `protocol` where a static pin requires
one, or obtain the signed declaration through normal manifest discovery.
