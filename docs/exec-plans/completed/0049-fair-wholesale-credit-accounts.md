---
title: Fair wholesale credit accounts and delegated invocation
status: completed
date: 2026-09-09
beads: lnm-b41
supersedes: none
---

# 0049 — Fair wholesale credit accounts and delegated invocation

## 1. Purpose

Replace request-ceiling ticket minting and `work_id`-owned residual balances
with stable payer-payee wholesale accounts. Tickets add only bounded account
shortfall. Single-purpose authorizations reserve account value for one job or
session, actual settlement debits it, and unused value returns to the account.

The design must support both an in-path provider and an out-of-path funding
intermediary whose customer invokes the broker directly. SDK callbacks improve
latency but are never required for correct accounting.

The governing design is
[`wholesale-credit-accounts.md`](../../design-docs/wholesale-credit-accounts.md).

## 2. Decisions

1. The stable economic owner is `(chain, payer, payee, denomination)`;
   `work_id` identifies ticket-validation generations only.
2. Maximum workload allowance is authorization, not ticket EV to mint.
3. The caller receives one route- and workload-bound spend authorization, not
   generic authority over pooled credit.
4. The receiver atomically admits, reserves, settles actual usage, releases the
   remainder, and exposes signed durable status.
5. The sender mints only the idempotent shortfall needed to maintain bounded
   aggregate float.
6. Broker lookup is authoritative when an SDK or raw HTTP caller omits the
   settlement callback.
7. Long-running sessions use cumulative authorization caps and small funded
   runway; account replenishment is aggregate rather than per-session maximum.
8. Customer pass-through and independent retail pricing remain payer-service
   policies backed by a ledger separate from wholesale accounting.

## 3. Work graph

Beads is authoritative for status and dependency edges:

| Bead | Deliverable |
|---|---|
| `lnm-b41.4` | Delegated-caller boundary decision |
| `lnm-b41.5` | Language-neutral account and authorization protocol |
| `lnm-b41.6` | Receiver account and reservation implementation |
| `lnm-b41.7` | Sender target-float and shortfall funding implementation |
| `lnm-b41.8` | Broker authorization, admission, debit, and settlement |
| `lnm-b41.9` | Conformance, migration, failure recovery, and operator guidance |

The protocol specification blocks the three component implementations. All
three implementations block final conformance and migration work.

## 4. Done looks like

- Issued ticket EV converges to settled charges plus configured reusable float,
  not the sum of request maxima.
- Concurrent jobs and sessions cannot over-reserve or spend each other's
  authority.
- A direct caller can omit every callback without causing incorrect accounting.
- Expired-unused authorization cannot later be admitted.
- Retry, crash, rotation, failover, and settlement recovery do not double-mint,
  double-credit, double-execute, double-debit, or strand value.
- New capabilities require no payment component release.
- Migration and operator exposure controls are documented and tested.

## 5. Outcome

Completed on 2026-09-09. The language-neutral contract, generated Go
bindings, sender and receiver daemon services, broker job/session paths,
funding-only endpoint, durable recovery, exposure metrics, migration fence,
and operator guidance shipped together.

Validation included all component Go tests and vet checks, deterministic
protobuf regeneration, the payment-daemon coverage gate, payment-daemon and
capability-broker production Docker builds, and the protocol conformance suite
(53 passed, 0 failed).
