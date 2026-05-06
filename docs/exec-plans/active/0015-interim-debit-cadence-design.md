---
status: design-doc
opened: 2026-05-06
owner: harness
related: plan 0014 (completed), plan 0011-followup, plan 0012-followup, plan 0016
audience: broker maintainers, payment-daemon operators
---

# Plan 0015 — interim-debit cadence on long-running modes (design)

**This is a paper-only design doc.** No Go code, no `go.mod` edits, no
proto changes ship from this commit. The goal is to land the contract,
the API surface, the configuration grammar, and the mode-by-mode
applicability matrix so the implementing commits in plan 0015
(forthcoming) are mechanical.

## 1. Status and scope

Scope: **broker-side periodic `DebitBalance` + `SufficientBalance` for
long-running mode sessions.** The broker grows a per-session ticker
that:

- Periodically calls `PayeeDaemon.DebitBalance` with the work units
  that have accumulated since the last tick.
- Periodically calls `PayeeDaemon.SufficientBalance` to confirm there
  is runway for the next tick(s).
- On insufficient balance, terminates the in-flight handler so the
  operator stops doing uncompensated work.

Out of scope (each is its own plan or its own followup, see §13 for the
full list): payer-side mid-session ticket top-ups; the
`rtmp-ingress-hls-egress@v0` and `session-control-plus-media@v0` media
planes themselves (those are 0011-followup / 0012-followup); chain
integration (plan 0016); per-mode tick-rate overrides.

The wire and RPC machinery for this plan already shipped with plan
0014: `DebitBalance(sender, work_id, work_units, debit_seq)` is
idempotent by `(sender, work_id, debit_seq)` per
`livepeer-network-protocol/proto/livepeer/payments/v1/payee_daemon.proto:215-218`,
and `SufficientBalance(sender, work_id, min_work_units)` is the
read-only credit check at `payee_daemon.proto:227-238`. Plan 0015 wires
the broker side; the daemon needs no further work.

## 2. Problem statement

Today the broker calls `DebitBalance` exactly once per session, at
handler completion. The single-shot pattern is hard-coded in
`capability-broker/internal/server/middleware/payment.go:125-135`:

```
// 3. DebitBalance. v0.2 issues exactly one debit per session
//    (debit_seq=1); plan 0015 will issue a sequence of
//    debits for long-running modes.
```

For the request/response modes (`http-reqresp@v0`, `http-multipart@v0`)
this is correct: the handler returns in tens to hundreds of
milliseconds. For the long-running modes the current behaviour leaves
the operator working on credit:

| Mode | Typical session length | Current debit cadence |
|---|---|---|
| `ws-realtime@v0` | minutes to hours | one debit at WS close |
| `rtmp-ingress-hls-egress@v0` | minutes to hours | one debit when the broker tears down (post 0011-followup) |
| `session-control-plus-media@v0` | minutes to hours | one debit at session-control teardown (post 0012-followup) |
| `http-stream@v0` (SSE / chunked) | seconds to minutes | one debit at body close |

Concretely: a `ws-realtime@v0` session opens at T0 (handler in
`capability-broker/internal/modes/wsrealtime/driver.go:58-128`),
relays frames bidirectionally for 1 hour, then closes. The middleware
in `payment.go:128-135` debits once at T0+3600s. If the payer's
session balance was sized for 5 minutes of work, the operator
over-extended for 55 minutes with no signal back from the broker to
cut the connection.

The session balance lives in the daemon's BoltDB ledger; the broker
has the only authoritative view of "work happening right now". Without
a tick-driven readout, neither side notices the gap until the
connection finally closes.

## 3. Tick driver

The driver lives in the payment middleware, after `OpenSession` and
`ProcessPayment` succeed (`payment.go:83-105`). One goroutine per
active session.

### 3.1. Lifecycle

Sequence per request: `OpenSession` (idempotent) → `ProcessPayment`
(seals sender) → spawn ticker goroutine → `next.ServeHTTP(...)` runs
in parallel with the ticker's `DebitBalance(delta_n, seq=n)` calls
plus periodic `SufficientBalance` checks → handler returns → ticker
stops → middleware does the final flush
`DebitBalance(delta_final, seq=N+1)` → `CloseSession`.

The ticker is **owned by the middleware**, not the mode driver. Mode
drivers expose a read-only counter (see §4); they don't touch debit
sequencing.

### 3.2. Tick cadence

New flag: `--interim-debit-interval` (duration). Default candidates:

| Default | Trade-off |
|---|---|
| 10s | Tight billing; up to 12 RPCs/min × concurrent sessions to the daemon. For 100 sessions, 1000 RPCs/min — fine. |
| 30s | Balanced; matches the operator runbook's `--redemption-interval` default (`payment-daemon/docs/operator-runbook.md:186`). 200 RPCs/min at 100 sessions. |
| 60s | Coarse; up to 1 minute of uncompensated overrun on the worst-case "balance just hit zero" tick. |

**Recommendation: 30s** for v0.1, mirroring `--redemption-interval`. It
keeps cadence tunables consistent across the daemon ecosystem and
keeps the worst-case overrun bounded to one tick (~30s of credit) plus
the daemon round-trip. The operator can lower for tighter billing or
raise for chattier sessions.

### 3.3. Termination signal

The ticker stops when **either** the handler returns **or** the
request context is cancelled (`r.Context().Done()`). Sequencing:

1. Handler returns.
2. Middleware signals the ticker to stop via a channel close.
3. Middleware reads the **final** accumulated work units from the
   counter.
4. Middleware computes `delta_final = current - last_tick_total` and
   calls `DebitBalance(delta_final, debit_seq = N+1)` synchronously.
5. Middleware calls `CloseSession`. (Already `defer`'d at
   `payment.go:108`.)

The final flush is **not** done by the ticker goroutine. It is done by
the main middleware path after the ticker returns, so we have a
single ordered chain (handler → flush → close) and no race between a
late tick and `CloseSession`.

### 3.4. Resource cost

One goroutine per active session. The broker's concurrency cap is
already set by the operator (max in-flight requests / max ws
connections). At 100 concurrent ws-realtime sessions, the ticker
overhead is 100 goroutines + 100 timers — well under the threshold
where a worker pool would beat per-session goroutines on memory or
scheduler load. Worker-pool optimisation is open question 4 in §11; v0.1
ships per-session goroutines.

## 4. Work-unit accumulation

The middleware needs to ask "how many work units have accumulated on
this session so far?" on every tick. The current extractor interface
in `capability-broker/internal/extractors/types.go:16-25` runs **once**
at response close — it takes a buffered request + response and returns
a count. There is no running counter.

### 4.1. New interface (design only)

We add a sibling interface to `Extractor`:

```
// LiveCounter is implemented by extractors that can be polled
// mid-flight for the running unit total. Mode drivers register a
// LiveCounter alongside their handler when they support interim
// debit; the payment middleware polls it every tick.
type LiveCounter interface {
    CurrentUnits() uint64  // monotonic; safe for concurrent reads
}
```

`LiveCounter` is **not** a replacement for `Extractor`. The
final-flush path still calls `Extract` after the handler returns to
produce the canonical end-of-session unit count. `LiveCounter` is the
interim view; `Extract` is the authoritative end view. The two MUST
agree at end-of-session — see §5.4 for the reconciliation rule.

### 4.2. Mode-driver wiring

`modes.Params` (`capability-broker/internal/modes/types.go:34-41`)
already carries `Extractor`. We add a sibling:

```
type Params struct {
    Writer     http.ResponseWriter
    Request    *http.Request
    Capability *config.Capability
    Extractor  extractors.Extractor
    LiveCounter extractors.LiveCounter // nil iff the driver does not support interim debit
    Backend    backend.Forwarder
    Auth       *backend.AuthApplier
}
```

The middleware checks `Params.LiveCounter` for nil. nil means
fall-through-to-single-debit behaviour (the v0.2 default).

### 4.3. Per-extractor implementation sketch

| Extractor | Live counter feasibility | Implementation |
|---|---|---|
| `bytes-counted` (`extractors/bytescounted/extractor.go`) | trivial | An `atomic.Uint64` incremented in the proxy loop (the `pumpFrames` loop in `wsrealtime/driver.go:133-147` for ws-realtime, or in the body-copy loop for `http-stream`). The counter is divided by `granularity` at read time. |
| `seconds-elapsed` (`extractors/secondselapsed/extractor.go`) | trivial | `time.Since(start) / granularity`, rounded per the configured rounding mode. No state — just a closure over `start`. |
| `ffmpeg-progress` (`extractors/ffmpegprogress/extractor.go`) | harder | Today the extractor parses a buffered final body. For interim, the FFmpeg subprocess (which lands in 0011-followup) must run a parser goroutine that keeps the `frame=` and `out_time_us=` values in atomic fields. The `LiveCounter.CurrentUnits()` reads those atoms and applies the unit conversion. The race-condition concern is open question 5 in §11 — atomic reads on uint64 fields are the recommendation. |
| `openai-usage` | not applicable | The `usage` block lands only in the final response. No interim view exists. |
| `response-jsonpath` | not applicable | Same — JSONPath against a buffered body. |
| `request-formula` | not applicable | Pure function of the request; doesn't change after the request lands. |

The "not applicable" set is exactly the set of extractors used by the
HTTP family in single-shot reqresp mode, which validates the
mode-by-mode cut in §8: `LiveCounter` is meaningful only for the
streaming/long-running modes whose extractor is one of the first
three.

### 4.4. Concurrency contract

`CurrentUnits()` MUST be safe to call from a goroutine that is **not**
the goroutine producing the count. The two trivial implementations
(`bytes-counted`, `seconds-elapsed`) satisfy this for free —
`atomic.Uint64.Load()` and `time.Since(start)` are both
goroutine-safe. The `ffmpeg-progress` parser must also use
`atomic.Uint64` for its frame/out_time fields. Open question 5.

## 5. Idempotency and delta accounting

`DebitBalance` is idempotent by `(sender, work_id, debit_seq)`
(`payee_daemon.proto:215-218`). `debit_seq` is a `uint64` chosen by the
caller. Two viable shapes for the broker side:

### 5.1. Option A — cumulative reporting

The tick reports cumulative work units; the daemon stores
`last_debited_cumulative_N` and computes the delta server-side:
`new_balance -= price × (current_N - last_N)`.

- **Pro:** simpler if a tick is dropped — the next tick's cumulative
  value re-syncs the daemon.
- **Con:** changes the daemon's `DebitBalance` semantics from "add N
  units to consumed" to "consumed reaches N". That contradicts the
  current proto comment at `payee_daemon.proto:206-218` ("Number of
  work units actually consumed").

### 5.2. Option B — delta reporting (recommended)

The tick reports the delta since the last tick; the broker keeps
`last_tick_total` locally. Each tick's `work_units = current -
last_tick_total`; broker updates `last_tick_total` on a **successful**
RPC return.

- **Pro:** matches the existing daemon semantics — `DebitBalance` adds
  N to consumed. No daemon change needed.
- **Pro:** idempotent retries against a flaky network are still safe:
  the broker reuses the same `debit_seq` until it gets a successful
  reply, and the daemon's idempotency guard ensures the same delta
  isn't applied twice.
- **Con:** if the broker crashes mid-tick, it loses `last_tick_total`
  and the next-process-incarnation can't continue the session. (This
  is an acceptable failure mode for v0.1: a broker crash terminates
  all WS connections anyway.)

**Recommendation: Option B.** Smaller blast radius, no daemon change.

### 5.3. `debit_seq` allocation

Per session, the broker maintains a monotonic `uint64` counter
starting at 1:

- Tick #1 → `debit_seq = 1`
- Tick #2 → `debit_seq = 2`
- ...
- Tick #N → `debit_seq = N`
- Final flush → `debit_seq = N+1`

The counter is per-`work_id`, scoped to the goroutine. No
cross-session collision concern because the daemon's idempotency key
is `(sender, work_id, debit_seq)` and `work_id` is per-request.

A retry of a failed tick **reuses** the same `debit_seq`. The broker
keeps the seq pinned until it sees a non-error reply.

### 5.4. Reconciliation at session close

At final flush, the broker reads `LiveCounter.CurrentUnits()` once
more to get the closing total, then **also** runs `Extract` on the
buffered request/response (where applicable) for the canonical count.
For ws-realtime / rtmp / session-control there is no buffered
response, so `LiveCounter` IS the canonical count. For `http-stream`
with `bytes-counted`, the two should agree to the byte; if they
disagree by more than a tunable epsilon, log a warning. Plan 0015 v0.1
does not implement an automatic reconciliation rewrite — it just logs.

## 6. SufficientBalance check and connection termination

On every tick (or every K ticks; see §7), the middleware calls
`SufficientBalance(min_work_units)` from
`payee_daemon.proto:227-238`. The minimum runway is configurable.

### 6.1. Runway sizing

New flag: `--interim-debit-min-runway-units` (uint64). Recommended
default: **2× the per-tick estimate**. The estimate itself is
session-specific — for `seconds-elapsed` with `granularity=1`, the
per-tick estimate is the tick interval in seconds.

Concretely, with `--interim-debit-interval=30s` and
`--interim-debit-min-runway-units` left at its default of `60` (2× 30
seconds for a `seconds-elapsed` work-unit), the broker checks every
tick: "does this session have at least 60 seconds of credit left?"

For the `bytes-counted` family the per-tick estimate is harder to fix
in advance. v0.1 punts: operators can override the flag per-broker
based on their typical session shape. A future plan can teach the
middleware to compute a rolling per-tick average.

### 6.2. Termination

When `SufficientBalance` returns `sufficient=false`, the middleware:

1. Cancels the request context (`r.Context()` derived child cancel).
2. For ws-realtime, that propagates into `pumpFrames`
   (`wsrealtime/driver.go:133-147`) which exits and closes both
   sockets.
3. For `http-stream`, the body-copy loop sees the cancelled context
   and stops.
4. For rtmp / session-control (post 0011/0012-followup), the media
   plane shutdown is plumbed off the same context.
5. The middleware logs the termination at WARN level and emits a
   `Livepeer-Error: insufficient_balance` trailer where the protocol
   allows (`livepeerheader.WriteError` is unavailable post-handler-start;
   trailer is the fallback).
   **Spec change required:** `insufficient_balance` is a new
   `Livepeer-Error` code. The canonical list at
   `livepeer-network-protocol/headers/livepeer-headers.md` and the Go
   constants at `capability-broker/internal/livepeerheader/headers.go:35-43`
   (currently nine codes) both grow by one as part of plan 0015's C2
   commit (see §12).

### 6.3. SufficientBalance polling rate

Calling `SufficientBalance` on every tick doubles the daemon RPC load.
Two options:

- **Every tick:** simple; doubles RPC volume.
- **Every K ticks** (K=2 or 3): `SufficientBalance` after every other
  successful `DebitBalance`. Halves the RPC overhead at the cost of a
  longer worst-case overrun window.

**Recommendation: every tick** for v0.1. Simpler, and the daemon RPC
cost is small (one BoltDB read with no ledger mutation). Revisit if
profiling shows daemon RPC throughput becoming the bottleneck.

## 7. Configuration surface

The broker grows three flags in plan 0015's implementation:

| Flag | Type | Default (recommended) | Purpose |
|---|---|---|---|
| `--interim-debit-interval` | duration | `30s` | Tick cadence. `0` disables the ticker entirely (broker reverts to v0.2 single-debit behaviour). |
| `--interim-debit-min-runway-units` | uint64 | `60` (2× the default tick at 1-unit-per-second) | Minimum work-unit runway for the `SufficientBalance` check. |
| `--interim-debit-grace-on-insufficient` | duration | `0` | (Open question 3 in §11.) If non-zero, the broker waits this long after a `sufficient=false` reply before terminating, giving a future top-up flow time to land. v0.1 default: zero (hard-terminate). |

Per-mode override discussion: rtmp ingest may want a finer cadence
(e.g., 5s) than ws-realtime in chat applications. v0.1 ships **one
global flag** to keep the surface small; per-mode overrides go in a
followup if operators ask. Open question 2.

## 8. Mode-by-mode applicability

| Mode | Spawn ticker? | Notes |
|---|---|---|
| `http-reqresp@v0` | No | Single request/response; the existing single-debit path is correct. |
| `http-stream@v0` | Conditional | Spawn the ticker **only when** the configured extractor is `bytes-counted` or `seconds-elapsed`. For `response-jsonpath` / `openai-usage` the runner doesn't have a meaningful interim view; trailer-debit at close still works. |
| `http-multipart@v0` | No | Same shape as reqresp at the broker boundary. |
| `ws-realtime@v0` | **Yes** | Primary use case. End-to-end exercisable today via plan 0010's driver pair. |
| `rtmp-ingress-hls-egress@v0` | Yes — gated on 0011-followup | The session-open phase (`rtmpingresshlsegress/driver.go:50-100`) doesn't have ongoing work; the driver returns 202 immediately. The actual RTMP listener + FFmpeg + HLS sink land in 0011-followup, and they are the things that produce work-units. The interim-debit machinery from 0015 is wired in and ready, but the LiveCounter implementation lives in the followup. |
| `session-control-plus-media@v0` | Yes — gated on 0012-followup | Same shape as rtmp: today's driver (`sessioncontrolplusmedia/driver.go:42-86`) is session-open only. The control-WS lifecycle and media-plane provisioning land in 0012-followup; the LiveCounter for the cadence-billed control plane lives there. |

### 8.1. v0.1 of plan 0015

For the first commit pass, **only `ws-realtime@v0` is fully
exercisable end-to-end**. The infrastructure (LiveCounter interface,
ticker goroutine, flag plumbing, conformance hook) is in place so
0011-followup and 0012-followup can plug in their counters with no
broker-core changes. `http-stream@v0` is a same-shape extension that
can land in 0015 v0.1 if it falls out of the ws-realtime work cleanly;
otherwise it ships in v0.2.

## 9. Conformance fixtures

A new fixture for the conformance runner under
`livepeer-network-protocol/conformance/fixtures/ws-realtime/`:

- **`interim-debit.yaml`** — runner opens a ws-realtime session, holds
  it for at least N tick intervals, sends frames continuously to keep
  bytes accruing, then closes.

Assertions the runner must make (these need new conformance hooks; see
§9.1):

- The daemon's session ledger received `≥ 2` `DebitBalance` calls.
- The reported `debit_seq` values are monotonic starting at 1.
- The sum of debited units matches the bytes the runner sent.
- The session's final balance after close matches `paid_ev - debited
  units × price`.

For a "credit runs out" assertion, a separate fixture:

- **`interim-debit-balance-exhausted.yaml`** — runner pays for a
  small balance, opens the session, the broker terminates within ~2×
  tick interval after balance hits zero. Assertion: the WS close came
  from the broker side, not the runner; the daemon shows balance ≤ 0.

### 9.1. Test-mode hooks

To run conformance in CI without holding sessions open for minutes,
the broker needs a way to accept very-short tick intervals. Two
candidates:

- `--interim-debit-interval=100ms`: works fine for fixtures; the
  fixture holds the session for ~500ms.
- A test-only flag `--interim-debit-test-min-interval=10ms` that
  removes the production lower bound (recommend 1s lower bound in
  prod).

**Recommendation:** no separate test flag; just allow
`--interim-debit-interval` down to `10ms` and warn at boot if the
value is below `1s` outside of test environments. The CI fixture
overrides via the broker compose config.

For the daemon-side observability (counting `DebitBalance` calls), the
conformance runner can read `GetBalance` between ticks and infer call
count from the balance trajectory. A more direct hook would be a
test-only `ListDebitsForWorkID` RPC; v0.1 does not add it because the
indirect inference works.

## 10. Operator runbook updates

`payment-daemon/docs/operator-runbook.md` lives on the daemon side but
operators read it for the system as a whole. Plan 0015 adds:

- **New §"Long-running session billing"** — describes the broker's
  ticker, the cadence flag, the `SufficientBalance` runway, the
  termination semantics, and the relationship to the existing
  `--redemption-interval`. Audience cross-references: the gateway
  operator setting `--interim-debit-interval` lives next to the
  receiver operator setting `--redemption-interval` in the operator's
  mental model — keep the two cadences described together so the
  operator can reason about end-to-end latency from "work performed"
  to "revenue recognised" (work → debit → ProcessPayment → ticket
  generated → win-prob roll → redemption queue → on-chain confirm,
  bounded by `interim-debit-interval + redemption-interval +
  redemption-confirmations × block-time`).

- **Update §7 Common failure modes** — add rows for:
  - "broker terminated session with `Livepeer-Error: insufficient_balance`"
    → payer's session balance hit zero; either a top-up flow is missing
    or the gateway sized the initial payment too small.
  - "DebitBalance call rate exceeds expected cadence" → broker is
    retrying tick deltas; check broker logs for tick failures.

- **Update §8 Observability** — add metrics:
  - `livepeer_payment_interim_debit_total{outcome}` (counter; outcome
    ∈ {success, retried, terminal_failure}).
  - `livepeer_payment_session_terminated_total{reason}` (counter;
    reason ∈ {balance_insufficient, handler_complete, ctx_cancelled}).

These are documentation deltas only; the metric wiring lands in plan
0015's implementation commits.

## 11. Risks and open questions

1. **Default `--interim-debit-interval`.** Recommend 30s. Is the
   operator's preference 10s (tighter billing, more daemon RPC) or 60s
   (looser billing, less RPC)? The recommendation aligns with the
   existing `--redemption-interval` default at
   `payment-daemon/docs/operator-runbook.md:186`; consistency is the
   primary argument.

2. **Per-mode tick rates.** The default proposal is one global
   `--interim-debit-interval`. RTMP ingest with frame-grain billing may
   want 5s; chat-style ws-realtime is fine at 30s. Do we ship a single
   global knob in v0.1 and add per-mode overrides if operators ask, or
   do we land per-mode from the start? Recommend single global v0.1.

3. **Grace period on `SufficientBalance` failure.** In v0.1, a
   `sufficient=false` reply terminates the connection within the next
   tick. A future "mid-session ticket top-up" plan would benefit from a
   grace period (the gateway sends a top-up `Payment` while the broker
   waits). The flag `--interim-debit-grace-on-insufficient` is
   reserved (default `0`); the top-up flow itself is a separate plan.
   Should v0.1 ship the flag wired-but-defaulted-off, or omit it
   entirely until the top-up flow lands? Recommend wiring it off-by-
   default; preserving the flag now keeps operator config stable.

4. **Goroutine count at scale.** One goroutine per active session is
   fine up to ~10k concurrent sessions. Above that, a worker pool
   (e.g., a single goroutine doing a min-heap of next-tick deadlines)
   is more efficient. v0.1 ships per-session goroutines; the
   threshold for switching is ~10k concurrent paid sessions per
   broker instance, which is well above what plan 0015 needs to
   exercise. Open question: do we ship a flag to switch between the
   two strategies, or hard-code per-session goroutines and revisit?
   Recommend hard-coded; a flag adds operator surface no one needs
   yet.

5. **Race conditions in `LiveCounter` reads.** For `bytes-counted` and
   `seconds-elapsed` the answer is `atomic.Uint64` /
   `time.Since(start)` and the reads are trivially safe. For
   `ffmpeg-progress`, the parser goroutine writes `frame=` and
   `out_time_us=` fields concurrently with `CurrentUnits()` reads.
   Recommend `atomic.Uint64` universally on both fields; the
   `LiveCounter` interface does the multiplication in `CurrentUnits()`
   under a single read each. (`frame_megapixel` reads only `frame`; the
   width/height are immutable from boot.)

6. **`--interim-debit-interval=0` semantics.** Document explicitly:
   `0` disables the ticker entirely and the middleware reverts to
   v0.2 single-debit behaviour. This gives operators a kill-switch if
   the ticker introduces a regression. Recommend yes; it's a small
   amount of code (a guard in the middleware) and zero ongoing
   maintenance.

## 12. Migration sequence

Estimated 2–4 commits, each independently reviewable:

1. **`feat(extractors): LiveCounter interface + bytes-counted /
   seconds-elapsed implementations (C1)`**
   - New `LiveCounter` interface in
     `capability-broker/internal/extractors/types.go`.
   - `bytes-counted` extractor exposes a `LiveCounter` via an
     `atomic.Uint64`.
   - `seconds-elapsed` extractor exposes a `LiveCounter` from a start
     timestamp.
   - `Params.LiveCounter` field added in
     `capability-broker/internal/modes/types.go`.
   - No middleware change yet; the field is unused.

2. **`feat(broker): interim-debit ticker + SufficientBalance check
   (C2)`**
   - Payment middleware in `internal/server/middleware/payment.go`
     gains the ticker goroutine.
   - `payment.Client` interface in `internal/payment/client.go` grows
     `SufficientBalance` (and probably `GetBalance` for observability).
   - GRPC implementation in `internal/payment/grpc.go` wires the new
     methods.
   - Mock implementation in `internal/payment/mock.go` wires them too.
   - New `Livepeer-Error` code: `insufficient_balance`. Added to
     `livepeer-network-protocol/headers/livepeer-headers.md` (canonical
     list) and to the Go constants at
     `capability-broker/internal/livepeerheader/headers.go:35-43`.
   - New flags: `--interim-debit-interval`,
     `--interim-debit-min-runway-units`,
     `--interim-debit-grace-on-insufficient` (off by default).

3. **`feat(modes): ws-realtime LiveCounter wiring + conformance fixture
   (C3)`**
   - `wsrealtime/driver.go` increments the bytes-counted atomic in
     `pumpFrames`.
   - The driver populates `Params.LiveCounter` when the configured
     extractor supports it.
   - Conformance fixture
     `livepeer-network-protocol/conformance/fixtures/ws-realtime/interim-debit.yaml`.
   - Conformance fixture
     `.../ws-realtime/interim-debit-balance-exhausted.yaml`.
   - `make smoke` and `make test-compose` green.

4. **`docs: runbook + close 0015 (C4)`**
   - `payment-daemon/docs/operator-runbook.md` updates per §10.
   - `PLANS.md` refreshed.
   - Plan file moves from `docs/exec-plans/active/` to
     `docs/exec-plans/completed/`.

C1 and C2 can ship as a single commit if the change is small enough;
the splits exist for review-tractability, not for technical
sequencing.

## 13. Out of scope (deferred)

- **Payer-side mid-session top-up flow.** The broker's `Livepeer-
  Error: insufficient_balance` trailer (§6.2) is the signal a future
  top-up plan would key off. The top-up itself — a gateway-side
  middleware that hears the trailer, mints another `Payment`, and
  retries — is its own plan.
- **rtmp-ingress / session-control interim-debit, end to end.** The
  broker-side ticker is in place after this plan; the LiveCounter
  implementations for those modes ship with their respective media-
  plane plans (0011-followup, 0012-followup).
- **Per-mode tick-rate overrides.** v0.1 ships a single global
  `--interim-debit-interval`. Per-mode overrides are a followup.
- **Test-only daemon-side `ListDebitsForWorkID` RPC.** Conformance
  uses indirect inference via `GetBalance`. Direct introspection is a
  followup if conformance assertions need it.
- **Reconciliation between `LiveCounter.CurrentUnits()` and the final
  `Extractor.Extract` count.** v0.1 logs disagreement at WARN and
  trusts the live counter. A future plan can rewrite the final debit
  to the canonical count if the gap is large.
- **Worker-pool / goroutine-pool optimisation.** v0.1 ships per-
  session goroutines.
- **Chain integration.** Stays stubbed per plan 0014's contract; plan
  0016 lights it up.

---

## Appendix A — file paths cited

Daemon contract: `livepeer-network-protocol/proto/livepeer/payments/v1/payee_daemon.proto:215-218` (`DebitBalance` idempotency), `:227-238` (`SufficientBalance`).

Operator context: `payment-daemon/docs/operator-runbook.md` (whole; cadence anchor at `:186`).

Broker code modified by 0015: `capability-broker/internal/server/middleware/payment.go:46-138` (single-debit flow; anchor at `:125-135`); `.../middleware/recorder.go:17-69` (trailer-fallback for final flush); `.../modes/types.go:34-41` (`Params` gains `LiveCounter`); `.../extractors/types.go:16-25` (`LiveCounter` sibling interface); `.../payment/client.go:21-43` (`Client` interface gains `SufficientBalance`/`GetBalance`); `.../payment/grpc.go` and `.../payment/mock.go` (adapter implementations).

Mode drivers: `.../modes/wsrealtime/driver.go:58-128` (Serve), `:133-147` (`pumpFrames` increment site); `.../modes/rtmpingresshlsegress/driver.go:42-100` (gated on 0011-followup); `.../modes/sessioncontrolplusmedia/driver.go:42-86` (gated on 0012-followup).

Extractors: `.../extractors/bytescounted/extractor.go:19-78` (atomic-counter sibling); `.../extractors/secondselapsed/extractor.go:19-77` (trivial); `.../extractors/ffmpegprogress/extractor.go:29-110` (atomic fields per §4.3).

Conformance: `livepeer-network-protocol/conformance/fixtures/ws-realtime/happy-path.yaml` (sibling fixtures land here).

Source of "interim-debit deferred to 0015" pointer: `docs/exec-plans/completed/0014-wire-compat-and-sender-daemon.md:128-132`.
