---
title: Durable session revision decisions and evidence
status: implemented
date: 2026-09-25
beads: lnm-0mtq, lnm-idmt, lnm-fq3p
---

# Session revision decisions and evidence

The gateway reports a refused successor on revision 747a085. The exact refill
rejection remains unconfirmed. LOC confirmed `settlement_replay`: the signed
terminal record omitted `settlement_seq` and decoded to zero. This change preserves the
cause before cancellation, makes revision outcomes independently verifiable,
and exercises multiple cumulative allowance increases. It does not relax
authorization validation or infer production accounting from current balances.

## Decisions and recovery

A durable revision decision separates outcome (pending, admitted, refused) from
stage, safe reason and receiver status code. Persist the decision to cancel before
calling the receiver; restart resumes cancellation rather than attempting admission
again. Preserve the original reason if cancellation temporarily fails. Only a
receiver fence or expired-unused record establishes final non-admission. Existing
admitted authority always wins a race with cancellation. Unknown receiver error
reasons remain pending; known reasons have an explicit disposition. Public errors
and logs contain allowlisted reason codes, never raw receiver errors or envelopes.

HTTP and WebSocket share the decision. Pending resolutions use
`revision_pending`; definitive refusals retain `refill_refused`. Retained revision
lookup returns the original outcome after response loss, independently of later
revisions. Identifiers are log fields, never metric labels.

## Attributable outcomes

An additive `SessionRevisionRecord` binds protocol, financial domain, broker and
gateway session IDs, request ID, payer/payee, predecessor/successor identities,
revision number, authorization hash, quote identity and immutable decision time.
Final outcomes are ADMITTED or NOT_ADMITTED. Non-admission additionally records
the receiver fence/expired-unused basis. It is not a zero-use settlement for the
whole session. LOC compares the complete scope against its issued grant and
verifies the same delegated broker signature envelope used for settlement.

`GET /v1/session/{id}/revisions/{request_id}` is directly queryable with the same
unguessable-identifier model as settlement lookup. Pending and historical records
without proof are unsigned. Final signed envelopes are persisted and replayed
verbatim. The generic non-admission endpoint must never attest across pending or
completed revisions; durable request coverage and atomic begin-versus-attestation
checks protect both old and new records, including after outcome eviction.

Final settlement continues to name the actual admitted authority and receiver
cumulative debit. A coarse runner claim may exceed its cap; that excess is
unbilled seller work, not a signature or accounting failure. Media shutdown stays
bounded while unresolved financial outcomes remain pending.

## Immutable terminal settlement

Allocate a positive broker session sequence and freeze the protobuf settlement
payload in the same transaction that writes terminal state. Receiver sequence15
orders receiver usage and settlement; it cannot substitute for the broker
session sequence. Cache the first signed envelope with compare-and-set against
the exact frozen payload. Repeated close and lookup preserve all evidence,
including timestamp, across restart and key rotation. Upgrade old retained
terminal records without a frozen payload once before publication. An unavailable
offering cannot change a previously frozen record.

The bounded 124-claimed/120-debited outcome remains intentional when the signed
cap and receiver accounting justify it. Add `claim_debit_gap_reason` to distinguish
`authorization_cap` from `unreconciled_claim`. LOC bills the verified120 and keeps
all signature, identity, accounting and replay checks. `canceled_unused` is a
durable, irreversible receiver fence for payer/authorization identity and exact
signed-byte hash within the durable settlement domain; the signed revision
envelope exposes that binding without claiming to settle the predecessor.

## Production observation

Read-only SSH inspection on 2026-09-25 confirmed the EU signed settlement for
LOC session `d21fde63-5803-43a9-81b4-ddd6b2b479a9`: predecessor authority,
closed, claimed124, debited/actual/billed120, and no `settlement_seq`. The deployed
image labels did not independently establish its source revision. Bounded
retained logs did not contain the original refusal. Only protected log/evidence
files were retained locally; no production service, ledger or key was modified,
and no database or key was copied. Current balances cannot reconstruct the
original refusal and the implementation does not claim otherwise.

## Validation boundary

Docker regressions exercise structured refusals, cancellation response loss,
restart, signed outcome verification and tamper rejection, retention and
non-admission races, multiple refills, and cap overshoot. Full broker/receiver
tests, race checks, protocol conformance and payment coverage gates apply.
The real-receiver fixture crosses four cumulative refill boundaries (120→180→
240→300→360), replays responses after restart, and closes at310 units. A separate
valid-grant/malformed-optional-funding fixture exercises refusal, durable
`canceled_unused`, predecessor exhaustion at124 claimed/120 billed, receiver
sequence15, broker terminal sequence1, and repeated close/restart. Its synthetic
refusal cause is not asserted to explain production. Existing expired-unused,
lost admission/settlement, stop-during-refill, and reservation106 regressions remain.

The signed JSON contract fixture lives in
`livepeer-network-protocol/conformance/fixtures/session-revision-evidence.json`;
its golden test verifies the exact generated envelopes. HTTP/WS regressions
cover pending versus refusal and directly queryable signed evidence. The
conformance suite grades positive terminal sequence and byte-identical
close/lookup evidence across broker restart.

Deployment and a paid OBS/RTMP-to-LOC acceptance test remain follow-up
`lnm-bdkk`. They require a separately authorized rollout and LOC integration;
local real-receiver tests are not a claim of uninterrupted production media or
LOC acceptance. Recover the original grant/decision if available or use the new
correlated diagnostics on a reproduction before assigning a historical cause.

Validation on 2026-09-25: Docker broker full tests and vet passed; receiver vet,
full coverage test run (including the secrets-log lint) and the 75% per-package
gate passed (receiver 78.5%). The real-receiver race target passed, as did targeted
server/store/engine/signing race tests. Protocol code generation/build passed;
the executable conformance suite passed 59/59 with no skips. The signed fixture
golden test and whitespace checks passed. No deployment or production repair was
performed.
