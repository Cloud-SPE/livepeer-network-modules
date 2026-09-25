---
title: Durable session capacity ownership
status: completed
date: 2026-09-25
beads: lnm-ur0g, lnm-hac1
---

# Durable session capacity ownership

Previously the broker checked an in-memory runner count while choosing a session
backend without reserving a session slot. Connect durable session ownership to the same
per-runner counters used by jobs. Acquire after request deduplication and before
payment or runner effects. Persist ownership before effects; reconstruct opening
and live session ownership before serving after restart, including old records.
The runner key is host plus local capability, shared by offers using that runner.
Zero max_in_flight retains the existing unlimited broker policy.

Release compute capacity after runner termination/absence is durably confirmed,
independently from financial closure. Retain all settlement obligations. Slot
release is idempotent by ownership identity. Creation timeouts without a known
runner ID cannot establish absence; retain their opening reservation and slot
until reconciliation establishes a runner outcome. Do not blindly replay create
against runners that do not guarantee create idempotency. Failed cleanup and an
unroutable runner cannot imply successful termination.

Recovery must not undo a live opening request when runners reattach; serialize
open and abandoned-open recovery per request. Persist runner-create attempt
before its RPC and retry cleanup for known runner IDs. Legacy paid opening
reservations cannot prove whether create ran, so retain uncertain capacity.

Jobs already reserve for their entire unary/multipart/response-stream exchange.
Use idempotent release and retry another eligible candidate when a concurrent
admission fills the first choice. The same handler covers HTTP and attached
WebSocket response streams. Request completion closes the transport; runner
cancellation remains a runner obligation and physical resource admission remains
enforced by the runner. This broker-local limit is not a cross-broker GPU lock.
Queueing is outside this change: queue_limit remains inactive and documentation
and examples must state that explicitly.

Regressions cover racing session opens, original-request replay at capacity,
refill, failed admission, failed termination, lost create responses, restart,
pre-upgrade records, payment outage after runner shutdown, and every job transport
holding its slot through response completion. Protocol fixtures describe the
observable capacity refusal and resource/settlement distinction.

Validation: Docker broker tests and vet pass. The real receiver revision suite
passes with the race detector, including broker engine/store/server and receiver
store/service checks. Protocol conformance passes all 59 scenarios. Capacity tests
cover 12 concurrent opens against two slots; starting work and replay at capacity;
refill; pre-admission rejection; failed cleanup; unknown create outcomes; restart
and old-record migration; lower limits; shared job/session counts; independently
pending settlement; unary, multipart and streamed jobs; WebSocket completion and
interruption; and selection/acquisition races with another runner available.

Unknown creation requires a future runner idempotency/lookup contract, tracked in
lnm-6026. The pre-existing initial authorization admission-intent recovery gap is
tracked separately in lnm-7uh1. Neither gap permits assuming runner absence in
capacity accounting. No production deployment is part of this implementation.
