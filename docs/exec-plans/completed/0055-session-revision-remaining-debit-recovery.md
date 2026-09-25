---
title: Cumulative session revision runway and recovery
status: completed
date: 2026-09-24
beads: lnm-ir9z
---

# 0055 — Cumulative session revision runway and recovery

Review: `lnm-x3ip`. Implementation: `lnm-ir9z` (Beads owns work status).
Implemented across capability-broker, payment-daemon and paid-session
conformance. Production deployment and ledger repair remain separate operations.

## Evidence and limits

The transcode gateway reports a successor allowing 180 units and 180 × 10^12
wei, with 74 units / 74 × 10^12 wei already billed and a requested runway of
120 units. Only 106 × 10^12 wei remains available under that authorization.

The source review before implementation confirmed the mechanism:

- [sessionRunwayReservation](../../../capability-broker/internal/server/session_routes.go)
  caps against the full successor debit and unit limits. Both HTTP and control
  WebSocket revisions pass that result to the session engine.
- [ReviseAuthorization](../../../capability-broker/internal/sessionengine/engine.go)
  sealed that amount before admission without subtracting inherited billing.
- [AdmitWholesale](../../../payment-daemon/internal/store/wholesale.go) inherits
  the predecessor's cumulative billing and rejects a reservation exceeding
  `maxDebit - inheritedBilled`. This guard is correct and must remain.
- [resumeRevisionLocked](../../../capability-broker/internal/sessionengine/revisions.go)
  repeated the persisted request and treated nearly every admission failure as
  retryable. Winddown terminated the runner but waited for this intent before
  settling payment, releasing capacity and becoming terminal.
- [receiver admission](../../../payment-daemon/internal/service/receiver/receiver.go)
  mapped the store's authorization-state rejection to `Internal`. Its admission
  fingerprint covers the signed authorization bytes, not the reservation.
  Existing admissions replay before optional payment processing.

This establishes a local defect, not the exact state of the reported EU session.
Before production repair, verify deployed broker and receiver revisions and
inspect a consistent, protected copy of the sealed intent through the store's
normal decryption path. Record sanitized identifiers, reservation, limits and
receiver status; do not expose authorization, payment or sealing secrets.

## Shared reservation calculation

Enforce the bound inside the session engine while holding the existing session
mutex. Request handlers may calculate desired runway, but admission and recovery
must share the final calculation.

Reconcile the current receiver authorization's cumulative billed amount, actual
units and payment sequence with durable broker progress. Resolve uncertain usage
advances before revising; never overwrite a pending operation using a stale
broker total or infer billing from aggregate account availability.

For a valid successor, apply:

`reservationWei = min(desiredRunwayWei, successorMaxDebitWei - cumulativeBilledWei)`

Also bound future units by `successorMaxUnits - cumulativeActualUnits`. Retain
integer wei arithmetic and cumulative ceiling billing for arbitrary price
denominators. Reject inconsistent totals that exceed the successor's limits.
Handle exhausted authority explicitly: the admission RPC currently treats zero
or omitted reservation as a request for the whole remaining debit, so zero must
not be used as a generic cancellation signal.

The reported case must reserve exactly 106 × 10^12 wei. It must not require a
new authorization or more wholesale credit merely to satisfy a 120-unit runway
configuration when only 106 units remain authorized.

## Durable revision recovery

Run the same resolver for new revisions, retries, startup and winddown. Preserve
request ID, authorization ID, predecessor, signed authorization bytes, payment
bytes, fingerprint and the promised lease. Repair only derived reservation and
reconciled accounting state, with durable writes before side effects.

| Receiver evidence | Resolution |
|---|---|
| Successor already admitted | Replay the same signed authorization to validate its fingerprint and obtain the accepted reservation. Reconcile cumulative state, then atomically commit the successor and idempotent response. Do not settle its superseded predecessor. |
| Successor absent, predecessor admitted | Reconcile predecessor billing, durably cap the old intent's reservation, and retry the same identity and payment bytes. Receiver transactional deduplication must handle an original admission completing concurrently. |
| Successor expired unused or definitively unadmitted | Persist a refused outcome and retain the predecessor as current. Winddown can settle that predecessor once non-admission is durable. |
| Successor settled | Validate the exact authorization fingerprint, adopt its cumulative state, and replay its confirmed settlement sequence. |
| Successor unexpectedly superseded or fingerprint conflicting | Hold the intent for authoritative reconciliation; do not guess which subsequent authorization owns settlement. |
| Receiver unavailable or outcome ambiguous | Retain the intent and financial obligations, retry with bounded backoff, and expose the unresolved state. Do not report a final settlement. |

A `NotFound` read alone does not fence an in-flight admission. If abandoning a
valid pending successor, require a receiver transaction that makes non-admission
durable before clearing the intent. Do not use the existing zero-use
`CloseUnexecutedAuthorization` shortcut for an admitted cumulative successor:
it can already contain inherited usage, and this session has a runner binding.

Keep authority replacement and the replayable response atomic, extending the
stored result shape for definitive refusals as necessary. A crash after the
reservation repair or after receiver acceptance must converge without duplicate
funding, reservation, billing or runner creation.

## Errors and finalization

Return expected admission-state failures as `FailedPrecondition` with stable
machine-readable reasons distinguishing excess reservation, predecessor state,
expiry and insufficient wholesale credit. Split the store's generic state error
where needed. Broker recovery must use typed outcomes and receiver evidence,
not match text or treat every `Internal` as a permanent refusal.

Keep runner termination independent of admission recovery, preserve the original
close reason, and settle only the reconciled active authorization. Persist the
receiver's cumulative billed amount, units, sequence and released reservation so
signed settlement agrees with the ledger even after a lost response. Only
publish a terminal result and release capacity once the durable shutdown
obligations have completed. Settlement remains discoverable by broker and
gateway session identifiers.

If 74 units is the final reconciled total, the final bill is 74 × 10^12 wei.
The 106 × 10^12 wei runway is a temporary reservation, not an additional charge.
Reported ABR failures from insufficient wholesale credit remain a separate
funding issue.

## Validation and operational delivery

Regression coverage must exercise the real receiver/store boundary: the current
broker revision fake accepts reservations without checking inherited billing.
Cover HTTP and WebSocket 180/74/120 arithmetic; cumulative rounding and exhausted
limits; pre-fix sealed intent restart; receiver acceptance with a lost response;
crashes around repair and commit; concurrent retries and pending usage; expiry
while stuck; definitive refusal; and winddown producing a verifiable cumulative
signed settlement with no duplicate charge or release.

Update paid-session conformance fixtures and the broker recovery runbook in the
implementation change. Production validation should compare the final receiver
authorization/account state with signed settlement, retained IDs and the
original close reason. Repair must use supported reconciliation code and a
protected backup, not ad-hoc Bolt edits or deletion of pending intents.

## Delivered behavior and verification

The shared engine resolver now repairs both new and previously sealed intents
using receiver cumulative billing under the session mutex. HTTP and WebSocket
top-ups share this path. Refusals and successful responses are durable and
idempotent; unresolved background retries persist a one-second exponential
schedule capped at one minute. Explicit retries may bypass the cooldown.

The additive trusted `CancelAuthorizationAdmission` RPC atomically fences an
unadmitted identity or returns existing authority after checking the hash of its
exact signed bytes. `GetSpendAuthorization` exposes a fence as
`SPEND_AUTHORIZATION_CANCELED_UNUSED`, allowing regional device drain to finish
without mistaking the fence for a missing ledger. Accepted cumulative usage is
never cleared by cancellation. Admission rejections carry `google.rpc.ErrorInfo`
in the `payments.livepeer.org` domain.

Settlement reconciles receiver units, billing and sequence, including lost
advance/admission/settlement responses, before signing the final result. A lost
or unavailable receiver ledger keeps financial finalization pending. Runner
termination proceeds independently, and status exposes the original close reason
while winding down. Regional accounting preserves the inherited billing baseline
when a previously accepted successor is recovered.

Validation completed in Docker:

- Full broker and payment-daemon package tests and `go vet ./...` passed,
  including payment-daemon's no-secrets-in-logs check.
- `make -C capability-broker test-revisions` passed with the race detector,
  using a real receiver gRPC service and persistent BoltDB. Regressions cover
  the 180/74/120 case, restarts, concurrent retries, response loss, receiver-ahead
  usage, refusal, winddown and verifiable signed cumulative settlement.
- Additional real-receiver tests passed for fractional cumulative pricing,
  zero remaining debit with units already paid by rounding, and regional drain
  after durable cancellation.
- Paid-session conformance passed all 58 scenarios. The shared
  [revision fixture](../../../livepeer-network-protocol/conformance/fixtures/session-revision-recovery.json)
  is exercised by HTTP and WebSocket regression tests.
- Payment coverage gates passed the 75% per-package floor (receiver 78.7%,
  store 76.4%). Protobuf bindings were regenerated from the updated contract.

Upgrade payment-daemon before capability-broker so cancellation/fencing is
available during recovery. The reported production revisions and sealed intent
have not been inspected, and production state has not been changed. Follow the
[broker recovery runbook](../../../capability-broker/docs/operator-runbook.md)
before applying this release to the affected session.
