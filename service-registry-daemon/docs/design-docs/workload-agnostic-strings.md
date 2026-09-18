---
title: Workload-agnostic identifiers
status: accepted
last-reviewed: 2026-09-14
---

# Workload-agnostic identifiers

Capability and offering identifiers are opaque strings. The registry uses
case-insensitive matching in selection and does not infer semantics from a
namespace, slash, colon or suffix. Reuse discovered identifiers exactly.

A naming convention such as `vendor:operation` helps people coordinate but
is not a reserved namespace registry or a validation enum. For example,
`openai:chat-completions` and `openai:/v1/chat/completions` are distinct keys.
Anyone may introduce a capability without a registry code change.

Workload behavior comes from the signed `protocol` tag and matching declaration
axes, interpreted by the gateway and broker. A capability without a slash does
not imply a streaming session. A protocol tag, not a naming heuristic, chooses
the consumer's interaction path.

The signed manifest is a flat list of capability/offering tuples. Each tuple
has an offering ID, work-unit name, nonnegative decimal price numerator and
optional `per_units` denominator (default 1). The resolver projects tuples into
nodes with nested capability/offerings lists; those lists are not the signed
wire format. A selectable route requires a matching offering ID.

Extra and constraints carry opaque JSON objects. The registry preserves them
and computes route fingerprints; it does not execute workload constraints.
Signed `protocol`, `job`, `session` and `settlement_domain_id` declaration keys
may not be shadowed by extra metadata. Advertised capacity is not a cross-workload routing guarantee;
consumers use live broker health and admission responses.

Use the protocol [offering axes](../../../livepeer-network-protocol/protocols/offering-axes.md)
and [manifest](../../../livepeer-network-protocol/manifest/README.md) as the
contract. Historical go-livepeer enum mappings in
[references](../references/capability-enum-mapping.md) are provenance, not a
current closed capability taxonomy.
