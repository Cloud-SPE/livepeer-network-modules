---
title: Session open reservation, retryable winddown, stream-safe backend timeouts
status: implementing
date: 2026-09-05
beads: lnm-z72
supersedes: none
---

# 0048 — Open reservation, retryable winddown, stream-safe backend timeouts

## 1. Purpose

Three broker defects the transcode team named on 2026-09-05, each
verified against the code and each real:

1. **The URL-backend client had a five-minute total timeout.**
   `http.Client.Timeout` counts the whole body, so a VOD or ABR encode
   longer than five minutes was cut before its terminal result and
   work-unit trailer. Attached (pool) runners were unaffected — the tunnel
   path has no such budget — but a standalone broker dialling its own
   runner by URL was.
2. **`Open` did not reserve the request id.** It looked the id up, then
   opened payment, processed the ticket, created the runner session, and
   only then persisted the record with the id. Two concurrent opens with
   one id could both pass the lookup and both perform payment and runner
   side effects before one lost the insert; a crash between payment and
   persist left a funded payee session with no record. Cleaning up the
   loser is not the no-second-debit guarantee paid-session promises.
3. **Winddown marked a session terminal whether or not its obligations
   completed**, and `Sweep` skipped terminal records — so the log line
   "payment close failed; will retry on sweep" described a retry that never
   ran. A runner session or an exclusive payee session could stay open
   forever, and capacity was released under it.

## 2. Decisions

1. **No total-response timeout on the backend client.** Bounds on
   connect (10s), response headers (60s) and body idleness (2m, reset per
   read) replace it. A stream that keeps producing is never cut; one that
   goes silent is.
2. **A durable open reservation.** `Open` writes `{request_id,
   fingerprint, stage}` to an `open_reservations` bucket before any side
   effect; a second open with the same id while the first is in flight
   gets `open_in_flight` (retryable); one with different content gets
   `request_id_reuse`. The reservation records each stage's identifiers
   (payment: work id, sender; runner: session id) as it passes, is deleted
   in the same transaction that persists the session, and is released on
   a failed-closed open. `Recover` sweeps reservations left in flight by a
   crash: it closes what the recorded stage says was opened, releases the
   capacity, and deletes the reservation — so a restart never leaves a
   funded session nobody holds.
3. **Winddown is durable until its obligations are done.** A winddown
   whose runner terminate or payment close fails leaves the record in
   `winding_down` with the close reason and per-obligation flags, keeps
   the capacity, and fires no outcome. `Sweep` retries it every tick;
   `Recover` retries it at startup. Only when both obligations succeed
   does the record become terminal, the capacity release, and the outcome
   report (plan 0045 decision 10) fire — once. A `winding_down` session
   refuses events and top-ups like a terminal one.

## 3. Implementation record

| § | Commit | What |
|---|---|---|
| 2.1 | `83802ba` | `backend.Timeouts`; idle-bounded body; tests for a long steady stream and a stalled one. |
| 2.2, 2.3 | `83802ba` | Reservation bucket and stages; `open_in_flight`; recovery of abandoned reservations; `winding_down` retry from Sweep and Recover; tests for concurrent opens, crash recovery, and a failed terminate that later succeeds. |
