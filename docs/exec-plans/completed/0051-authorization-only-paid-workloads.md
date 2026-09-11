---
title: Authorization-only paid workloads
status: completed
bead: lnm-4zb
---

# 0051 — Authorization-only paid workloads

## Decision

The stable payer–payee wholesale account is the only accounting model for
paid work. A probabilistic ticket transfers value into that account; it never
authorizes a workload by itself. Every `paid-job/v1` invocation and every
`paid-session/v1` open or cap revision carries a single-purpose
`SpendAuthorization`.

This supersedes plan 0049's negotiated migration state. There is no
`extra.features.wholesale_accounts` switch and no payment-only fallback.

## Shipped invariants

1. `Livepeer-Authorization` is mandatory at every paid workload admission.
2. The authorization binds payer, payee, broker, protocol, capability,
   offering, accepted quote, request/session identity, caller proof, maximum
   units/value, validity, and workload commitment.
3. `Livepeer-Payment` is permitted only at the account-funding endpoint or as
   optional shortfall funding accompanying a valid authorization.
4. Funding without authorization executes no work. Authorization without
   sufficient account value executes no work.
5. Settlement debits actual measured units, releases unused reservation, and
   is signed with authorization and quote identity.
6. Broker production paths contain no legacy workload-accounting branch.
7. Existing payment-only work must drain before deployment; old durable state
   is never assigned fabricated authorization history.
8. The cutover is coordinated and whole-stack; mixed versions fail closed.

## Implementation

- `paid-job/v1`, `paid-session/v1`, headers, wholesale-account, offering axes,
  and manifest 3.0.0 define the authorization-only contract.
- The capability broker admits, advances, and settles only account-backed
  authorizations. Payment bytes can only fund a shortfall.
- Session successor authorizations replace ticket-session refill, rebind, and
  recipient-rotation workload semantics.
- The payment sender requires `AccountFundingIntent`, computes only the account
  shortfall, and enforces `FundingIntent.funded_value_wei` as its mint ceiling.
- Conformance and integration scenarios exercise the sole account-backed path
  and explicitly reject payment-only jobs and session opens.
- Configuration has no wholesale-account feature flag or `max_rotations`
  compatibility axis. Operator guidance requires drain/deploy/rollback.

## Verification

- capability-broker: `go test ./...` and `go test -race ./...`
- payment-daemon: `go test ./...` and `go test -race ./...`
- pool-controller: `go test ./...` and `go test -race ./...`
- service-registry-daemon, orch-coordinator, secure-orch-console: `go test ./...`
- protocol: `make proto && make check`
- conformance: 56 passed, 0 failed, 0 skipped
- integration scripts: `bash -n infra/scenarios/integration-stack/*.sh`
- repository: `git diff --check`
