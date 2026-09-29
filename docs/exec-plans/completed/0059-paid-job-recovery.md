---
title: Durable paid-job admission and settlement
status: completed
date: 2026-09-26
beads: lnm-q0ik, lnm-ffig
---

# Durable paid-job admission and settlement

The job lifecycle owns runner capacity before payment admission. It reserves one
backend atomically after transport and signed-scope validation, then seals exact
authorization/funding bytes before any receiver side effect. Every return releases
that capacity once. Runner-side refusal after admission remains a distinct,
confirmed-zero settlement path.

The store records an execution marker before dispatch, measured usage before
settlement, and the signed envelope before publication. Receiver atomic admission
fencing resolves lost admission responses: unused authority becomes durably
refused; confirmed admission with no dispatch marker settles zero. Recovery never
reexecutes a runner. Read-only `GetWholesaleFundingReceipt` recovers credited
metadata without processing tickets. Missing receipts and older receivers without
the RPC keep this recovery pending.
Account identity is preserved throughout, including another account with identical
payer and authorization IDs.

A dispatch marker without durable measured usage is intentionally unresolved.
Generic job runners have no durable result lookup, so neither a crash, transport
failure nor a failed usage write proves zero execution. These records survive
retention and remain HTTP 202 ADMITTED_OUTCOME_UNKNOWN. Automatic reconciliation
requires a future runner evidence contract (lnm-2e08); this change prevents false
settlement
and preserves the evidence required for controlled reconciliation.

Terminal jobs persist one envelope with sequence 1 and a fixed issuance timestamp.
Replay and lookup return the saved bytes. Existing unsealed records remain readable;
rewrites seal sensitive job fields. Downgrades require draining outstanding jobs.

Validation passed in Docker: full broker and payment-daemon tests, vet and builds;
the real-receiver race gate; nine paid-job fault/restart scenarios including lost
funded admission, repeated signed replay and account isolation; read-only funding
receipt validation during source freeze; protocol verification/binding tests; and
all 59 conformance scenarios. Store tests verify sealing and retention of unknown
execution; transport tests assert capacity refusals make no receiver admission.

Deploy the receiver before the broker to enable the additive receipt lookup RPC.
No deployment was performed as part of this implementation.
