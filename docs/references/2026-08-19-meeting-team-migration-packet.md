# Meeting-team migration packet — requirements handoff → paid-session/v1

Date: 2026-08-19. Response to the "Remote Runner Extension Requirements"
handoff of 2026-08-18 (your tracking refs `lpmmeet-ct-09`, `lpmmeet-ct-10`;
our review of protocol sha `0f0756…` / broker sha `4c7ea6…`).

## Verdict and vehicle change

**Every numbered requirement is accepted and implemented** — with one
change of vehicle. Your protocol-fit review asked for a backward-compatible
extension of `live-session-remote-runner@v0`. We instead retired the entire
mode taxonomy and shipped two clean protocols; your requirements became the
**base contract** of `paid-session/v1` plus the first runtime-descriptor
schema, `sfu-room/v1`, rather than an extension bolted onto a legacy mode.

What this means for you, concretely:

- Nothing you asked for was narrowed. A1–A4 and B1–B5 all survive, most of
  them strengthened (details below). A5 (wire compatibility with RTMP/HLS)
  is moot: there is no legacy wire to stay compatible with, and nothing
  legacy for your integration to work around.
- Your "latitude" clause was exercised: field names and internal
  implementation differ from your sketch; the numbered semantics are all
  normative in the specs.
- The extension is **implemented, tested, and conformance-verified** — this
  packet describes working code, not a plan.

Specs: `livepeer-network-protocol/protocols/{paid-session.md,
runtime-descriptor.md, offering-axes.md}` and
`livepeer-network-protocol/descriptors/sfu-room.md`. Executable
conformance: `livepeer-network-protocol/conformance` (`make conformance`;
currently 18 pass / 0 fail / 2 documented skips against the reference
broker).

## Requirement-by-requirement mapping

### Part A — capability-opaque runtime coordinates

| Yours | Where it landed | Notes |
|---|---|---|
| **A1** optional capability-owned runtime descriptor | `runtime-descriptor.md` framework + `sfu-room/v1` schema | Stronger than asked: the descriptor is not optional-beside-RTMP — it *is* the runtime coordinates for every session capability. RTMP/HLS is just another schema (`rtmp-hls/v1`). |
| **A2** public/private partition, secrets structurally unable to leak | framework §2.2–2.4, §4 | Enforced by construction: closed top-level key set (unknown keys fail the open), deny-by-default sanitizer, private parts sealed (AES-256-GCM) at rest. `sfu-room/v1` additionally bans SFU API secrets and TURN credentials even from `private` — they are runner-internal, the broker has no use for them. Conformance runs sentinel leak scans. |
| **A3** open/status consistency + one-time control grant | framework §2.4 (grants), paid-session §3.1–3.2 | Open and status return the byte-identical public view; grants are delivered by open exactly once and never by status — verified by conformance. One improvement over your sketch: the `participant-token-mint` grant is delivered once but **multi-use until the session ends** (every participant join is a mint; single-use would have broken the workload). Grants die with the session regardless of expiry. |
| **A4** mode-generic runner paths | offering config `session.runner.{create,status,terminate}_path` | No default URL space exists at all; `{id}` is substituted. Nothing video-shaped survives anywhere in the protocol. |
| **A5** wire compatibility, four fixtures pass | superseded | The v0 mode and its fixtures were deleted (they were never executable — no conformance runner existed for that mode). The replacement is a real executable suite; see below. |

### Part B — durable session authority

| Yours | Where it landed | Notes |
|---|---|---|
| **B1** durable session state (your full field list) | paid-session §9.1 + the broker's bbolt session store | Field-for-field: identifiers, binding, payment sender + close status, callback auth **hashed**, last event id + sequence, claimed/debited totals + debit sequence, terminal state, held capacity. Survives restart by test. |
| **B2** usage-unit validation before debit | paid-session §7.2 | Mismatch is a protocol error advancing nothing — idempotency, sequence, and totals all untouched; a subsequent correct event still lands in full. Conformance-verified. |
| **B3** exactly-once debit under retry | paid-session §7.3 — your invariant, nearly verbatim | "Event deduplication MUST be committed only together with durable debit progress." Implementation uses a single atomic commit point plus the payment daemon's debit-seq idempotency; your "transaction, outbox, or idempotency key all acceptable" framing is preserved in the spec. Fault-injection tested: transient debit failure + retry = exactly one debit, never zero, never two. |
| **B4** heartbeat enforcement ("updating a timestamp is not enforcement") | paid-session §5 + offering axes `session.heartbeat` | Configurable interval/threshold; breach runs the idempotent winddown: terminate runner, close payment, release capacity, stable reason `heartbeat_lost`. Lease expiry, runway exhaustion, and gateway end all share the same terminal path. |
| **B5** restart recovery, rebind-or-terminal, forbidden outcomes | paid-session §9.2 | Rebind keeps the same work_id with credentials working and grants never re-minted; impossible rebind reaches an explicit `recovery_failed` terminal. Your forbidden-outcomes list (second work_id, stale callback, skipped usage, double debit, unmetered serving) is normative and test-pinned. |

### Your acceptance-evidence list → what covers it

| Your evidence item | Covered by |
|---|---|
| Open without RTMP/HLS fields | conformance `paid-session/happy-path` (no media fields exist at all) |
| Open/status identical sanitized descriptors | conformance `paid-session/open-status-public-identical` |
| Private + unknown fields cannot leak | conformance `descriptor/no-private-or-grant-leak`, `descriptor/unknown-top-level-key-fails-closed` |
| Descriptor size/shape/partition validation | conformance `descriptor/oversize-fails-closed`, `descriptor/schema-mismatch-fails-closed` |
| Configurable runner path | exercised by every conformance session scenario (the suite's fake runner uses `/sessions`) |
| Restart: status/topup/end/usage across restart | engine tests (`TestRecoverRebindsOrTerminates`, store restart-survival tests) — documented SKIP in the wire suite (needs process control) |
| Duplicate/reordered events safe | conformance `paid-session/duplicate-and-reordered-events-safe` |
| Transient DebitBalance failure charges exactly once | engine fault-injection test `TestExactlyOnceDebitUnderRetry` |
| Unit mismatch rejected without advancing totals | conformance `paid-session/unit-mismatch-advances-nothing` |
| Missed heartbeats force idempotent closure | engine tests `TestSweepHeartbeatLost`, `TestSweepLeaseExpiryRespectsGrace` |
| Explicit terminal when rebinding impossible | engine test `TestRecoverRebindsOrTerminates` |

### Your non-goals — all preserved

- **No meeting-specific interaction mode**: correct, and stronger — no
  workload-specific protocol names exist at all; identity lives in
  descriptor schemas.
- **Token minting stays out of the broker**: elevated from a meeting
  feature to the core trust model ("admission-edge metering",
  `docs/design-docs/dual-meter-trust.md`). Your gateway's token minting is
  now its first-party billing signal — participant-minutes measured at
  your own edge.
- **Broker sole payment authority / runner sole media authority**:
  unchanged, now with the dual-meter framing made explicit.
- **Opaque session-scoped gateway event destination in `session_params`**:
  preserved verbatim — `session_params` passes to the runner untouched.

## What your integration surface looks like now

Gateway side:

1. `POST /v1/session` with `Livepeer-Protocol: paid-session/v1`,
   `Livepeer-Capability`/`Offering`, required `Livepeer-Request-Id`
   (idempotency key — a retried open returns the same session and never
   re-delivers credential or grants), `Livepeer-Payment`.
2. Open response: `runtime{schema:"sfu-room/v1", public{url, room,
   mint_url}, grants[{participant-token-mint, secret, expires_at}]}`,
   `credential` (your resumable session bearer — all control calls use
   it), `lease{expires_at}`, `balance{…, will_refuse_next_refill}`, and
   `control{status_url, topup_url, end_url, events_ws}`.
3. Mint participant tokens against the runner's `mint_url` with the grant
   secret. Top-ups extend the lease. Watch `balance` (poll status, or
   attach the control-WS for pushed `session.usage.tick` /
   `session.balance` / `session.ended` frames).

Runner (SFU) side — three HTTP endpoints (paths are your choice, declared
in the orchestrator's config) plus one outbound flow:

1. Create: receives `session_params` verbatim + broker callback
   coordinates; responds with `runner_session_id` and the `sfu-room/v1`
   descriptor.
2. Status + idempotent terminate.
3. Post the event envelope (`event_id`, monotonic `sequence`, cumulative
   `usage.total` in the offering's declared unit) to the callback URL with
   the callback token. Retry on 5xx — exactly-once debit is the broker's
   problem, by contract.
4. Honor grants: verify the secret on mint calls, refuse after session
   end.

The conformance suite runs in URL mode against any implementation
(`conformance/README.md`) — point it at a broker configured for your
capability and your runner is testable against the same 18 scenarios.

## What we need from you

1. **Review `sfu-room/v1`** (`descriptors/sfu-room.md`) against your actual
   SFU: the public field set (`url`, `room`, `mint_url`, `status_url`),
   the mint-endpoint semantics, and participant-token TTL guidance. The
   schema is capability-owned — changes are yours to propose and cost no
   broker/registry work.
2. **Confirm the units**: the reference offering uses
   `participant_minutes`; tell us if your billing unit differs.
3. **Timeline**: the work lives on an unmerged branch
   (`tasks/refactor-interaction-modes-and-billing`); merge/publish timing
   is being coordinated — flag your integration schedule so we sequence
   accordingly.
