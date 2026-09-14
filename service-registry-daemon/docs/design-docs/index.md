# Design documentation

Active documents describe current behavior and explicitly identify gaps between
protocol requirements and implementation. `status` is proposed, accepted,
verified or deprecated; `last-reviewed` records a human/agent review date. The
linter checks frontmatter dates and file links, not semantic correctness,
anchors, Mermaid rendering or whether code changed after a review.

- [Core beliefs](core-beliefs.md): invariants and current enforcement limits.
- [Architecture](architecture.md): ownership and information flow.
- [Discovery modes](serviceuri-modes.md): chain pointers, coordinator URLs, CSV and static pins.
- [Static overlay](static-overlay.md): parsing, trust policy and configuration.
- [Cache](resolver-cache.md): synchronous refresh and bounded failure fallback.
- [gRPC design](grpc-surface.md) and [consumer contract](../product-specs/grpc-surface.md).
- [Manifest contract](../product-specs/manifest-contract.md): implementation checks and outstanding expiry/replay enforcement.
- [Protocol manifest](../../../livepeer-network-protocol/manifest/README.md) and [schema](../../../livepeer-network-protocol/manifest/schema.json): signed wire format.
- [Observability](observability.md): metrics and operational limits.
- [Identifiers](workload-agnostic-strings.md): workload-agnostic matching.
- [Broker offerings](worker-offerings-endpoint.md): runner/broker/coordinator ownership.
- [Adding a workload](adding-a-new-workload.md): integration path.

`docs/references/` and `docs/exec-plans/completed/` are immutable historical
provenance. Their relative links may reflect old repository layouts and are
excluded from active-document validation. They are not current API references.
The old manifest/signature docs under `references/archived/` have been
superseded by the protocol manifest linked above.

Beads tracks work. Execution-plan documents explain design decisions; current
product documentation links to durable contracts rather than plans.
