---
title: Paid-session output health and fail-closed stalled sessions
status: implemented
date: 2026-09-09
beads: lnm-13a
supersedes: none
---

# 0050 — Paid-session output health

## 1. Purpose

Runner heartbeat liveness is not proof that a long-lived workload is producing
useful output. The live transcode runner can remain responsive while every
ladder process fails, leaving a paid session active with zero billable output.
It now reports an optional output-health vocabulary, but paid-session/v1 treats
`details` as disposable and the reference broker has nowhere durable to keep
that state.

This plan adds a generic, optional output-health seam without teaching the
broker what an HLS ladder is. `rtmp-hls/v1` owns its ladder event and safe
failure-code vocabulary; paid-session owns durable event ordering, the common
output state, terminal behavior, and status projection.

## 2. Decisions

1. **Runner liveness and output health are distinct.** Every accepted event
   still refreshes runner liveness. Only an event declaring
   `details.output_state: producing` proves productive output. A stalled event
   therefore keeps a responsive runner out of `heartbeat_lost` without
   laundering the stall into healthy output.
2. **Output health is an optional paid-session extension.** `details` may carry
   `output_state` (`waiting`, `producing`, `stalled`),
   `output_state_since` (RFC3339), and a bounded safe `last_failure_code`.
   Runners that omit the extension remain valid and status reports `unknown`.
   Once a runner supplies it, malformed standardized fields reject the event
   without advancing its watermark.
3. **The event commit remains one atomic unit.** Latest output health and
   productive-output time move in the same store update as event id, sequence,
   usage, and debit progress.
4. **Terminal reasons survive.** A valid safe `close_reason` on
   `session.failed` is authoritative; `runner_failed` is only the fallback.
   `output_failed` is the standard reason for a session that cannot produce.
5. **Persistent stall fails closed.** A runner declaring `stalled` opts into a
   broker backstop: if it remains stalled for 60 seconds, the ordinary durable
   winddown runs with `output_failed`. The runner may and should fail sooner.
   `waiting` is not timed by this rule because many session workloads can wait
   legitimately for input.
6. **Workload vocabulary stays with the descriptor.** `session.ladder.restart`
   and the ladder failure-code vocabulary are specified by `rtmp-hls/v1`.
   The broker accepts it as an open-world event and interprets only the common
   output-health detail fields.
7. **Compatibility is explicit.** Old runner to new broker means unknown
   output health and the existing heartbeat policy. New runner to old broker
   receives tolerant 2xx acknowledgements but no output-health persistence or
   enforcement, so broker rollout precedes relying on the extension.

## 3. Delivery

The protocol revision, descriptor extension, reference broker implementation,
conformance fixtures, operator documentation, and alerting ship together. The
live transcode 75-second publish is an integration/certification exercise: it
must either observe at least 60 `output_seconds` or observe terminal
`output_failed` with a safe code within 60 seconds of authenticated ingest.

## 4. Implementation record

| Commit | What |
|---|---|
| `cc537ca` | Paid-session 1.2 output-health contract; `rtmp-hls/v1` 1.1 events; atomic broker persistence; status/WS/metrics/operator exposure; safe terminal reason preservation; 60-second stalled-output backstop; pool outcome mapping; unit, race, Docker smoke, and executable conformance coverage. |
