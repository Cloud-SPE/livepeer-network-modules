---
title: Adding a workload
status: verified
last-reviewed: 2026-09-14
---

# Adding a workload

New workload identities do not require a registry code change. Choose opaque
capability and offering identifiers, and make the runner, broker and consumer
agree on the selected interaction protocol and billing units.

## Supply side

Implement the [runner contract](../../../livepeer-network-protocol/protocols/runner-contract.md)
and outbound [attach flow](../../../livepeer-network-protocol/protocols/runner-attach.md).
The runner reports supported capability shape, paths, readiness and metering
information. The broker handles credential validation, certification,
eligibility and work dispatch. Operators define offers and wholesale prices;
runner software is not authoritative for published prices.

A work unit names what is billed. The signed price is an integer wei numerator
plus `per_units` denominator. If a client needs a special method to compute its
funding ceiling, use the typed estimator contract. The consumer must implement
that estimator; guessing a different ceiling is not equivalent.

The coordinator scrapes broker offerings and composes the manifest candidate.
The operator signs on the cold host, then uploads the signed result to the
coordinator. Gateways discover it through chain pointers or configured
coordinator URLs. New prices/capabilities require a new signed publication;
editing live inventory alone does not update trusted discovery.

## Consumer side

Use ResolveByAddress to inspect inventory and Select/SelectMany with the
published capability and offering keys to obtain complete routes. Implement
the signed protocol/axes, dispatch to the returned broker URL, and follow the
payment-daemon/broker contract. Unknown protocol or estimator declarations may
require consumer changes even though the registry needs none.

Retail pricing belongs to the gateway. It does not replace the signed
wholesale price. Empty-price signed tuples are rejected by the current
boundary validator; a consumer should not expect to fill their price later.

## Validation and references

Exercise runner attach/certification, coordinator publication, signed registry
resolution, targeted broker health, consumer protocol compatibility and paid
work separately. A route appearing in inventory is not evidence that all those
steps succeeded. The registry's in-process example checks discovery, not paid
work submission or on-chain redemption.

- [Manifest schema](../../../livepeer-network-protocol/manifest/schema.json)
- [Broker offerings](worker-offerings-endpoint.md)
- [gRPC route contract](../product-specs/grpc-surface.md)
- [Payment interactions](../../../docs/design-docs/payment-daemon-interactions.md)
- [Backend health](../../../docs/design-docs/backend-health.md)

Use beads for implementation work; do not add new task lists to historical
tech-debt documents.
