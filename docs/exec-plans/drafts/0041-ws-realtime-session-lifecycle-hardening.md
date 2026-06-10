---
status: draft
opened: 2026-06-10
owner: harness
related: plan 0015 (interim-debit cadence, completed), plan 0021 (vtuber-topup-control-ws), livepeer-network-protocol/modes/ws-realtime.md
audience: broker maintainers, payment-daemon operators, conformance authors
---

# Plan 0041 — ws-realtime@v0 session-lifecycle hardening

**Status:** draft. Addresses five gaps between the `ws-realtime@v0`
spec (`livepeer-network-protocol/modes/ws-realtime.md`) and the shipped
implementation (`capability-broker/internal/modes/wsrealtime/driver.go`,
`capability-broker/internal/server/middleware/payment.go`). Plan 0015
shipped interim-debit cadence; this plan closes the lifecycle behaviours
0015 explicitly deferred or left as follow-ups.

## 1. Scope

In scope — the five items below, all `ws-realtime@v0`:

1. **Idle & max-session timeouts.** Spec §Timeouts; not implemented in
   the relay loop.
2. **`Livepeer-Balance-Low` warning.** Spec §5 step "If the running
   balance falls below `runway_min_units`, broker emits a
   `Livepeer-Balance-Low` application-level message … and continues";
   not implemented. Metric `livepeer_mode_session_balance_low_events_total`
   is declared in the spec but has no emitter.
3. **Test realism.** Conformance fixtures exercise the path only against
   the `credit_ev=0` stub daemon, so there is no green long-running
   session and no real balance-low crossing.
4. **Per-offering cadence config.** `cadence_seconds` / `runway_min_seconds`
   (and the new timeout knobs) are broker-global CLI flags, not the
   per-offering `extra.*` overrides the spec table (§Default cadence
   parameters) implies.
5. **Close-frame error propagation.** Backend dial failure collapses to
   `CloseTryAgainLater`; balance termination cancels the handler context
   with no close frame carrying `Livepeer-Error: payment_invalid` (spec
   §5 step "If balance hits zero, broker initiates close with
   `Livepeer-Error: payment_invalid`").

Out of scope: mid-session ticket top-up (plan 0021 territory — this plan
only *signals* low balance, it does not refill); multi-broker failover /
session migration; reconnection/idempotency for dropped sockets; the
non-WS long-running modes (RTMP, session-control) — they share the
ticker but have their own media planes and fixtures.

## 2. The cross-cutting problem: ticker and socket live in different layers

Four of the five items (1, 2, 4, 5) are blocked on one structural fact:
the **interim-debit ticker runs in the payment middleware** and the
**WebSocket connection lives in the driver**, and today they share only
a one-way channel — the `LiveCounter` the driver writes and the ticker
reads.

- Ticker: `runInterimDebitTicker` in
  `capability-broker/internal/server/middleware/payment.go:479-577`.
  It can `DebitBalance`, `SufficientBalance`, and `cancelHandler()`, but
  it holds **no reference to the gateway socket**, so it cannot send a
  `Livepeer-Balance-Low` frame (item 2) or a reason-bearing close frame
  (item 5).
- Driver: `Serve` / `pumpFrames` in
  `capability-broker/internal/modes/wsrealtime/driver.go:62-169`. It
  owns both sockets but only learns of termination as a bare
  `relayCtx` cancellation (via `handlerCtx`), with no reason attached.
- The termination reason *is* already recorded — the ticker stores it in
  `terminationReason atomic.Pointer[string]`
  (`payment.go:307,570-572`) — but only the middleware reads it, after
  the handler returns (`payment.go:399-403`). The driver never sees it.

**Decision (D1): add a per-session control channel to `SessionState`.**
`SessionState` (`payment.go:64-68`) is the existing goroutine-safe
hand-off between the two layers and is already in the request context.
Extend it with a buffered control channel the ticker writes session
events to and the driver selects on:

```go
// payment.go — SessionState additions
type SessionEvent struct {
    Kind   string // "balance-low" | "terminate"
    Reason string // livepeerheader.Err* for terminate; "" for balance-low
}

func (s *SessionState) Events() <-chan SessionEvent // driver side, lazily created
func (s *SessionState) emit(ev SessionEvent)        // ticker side, non-blocking
```

The channel is buffered (cap 1 per kind, coalescing) and `emit` never
blocks the ticker — if the driver is slow, balance-low events drop
(the next tick re-sends). `terminate` is also mirrored to the existing
`cancelHandler()` so the relay still tears down even if the driver
missed the event. This keeps the ticker authoritative for *accounting*
and makes the driver responsible only for *wire signalling*, matching
the spec's "broker emits … and continues" / "broker initiates close"
split.

This channel is the shared substrate for items 2 and 5; item 1 lives
entirely in the driver; item 4 feeds parameters into both.

## 3. Item 4 — per-offering config (do this first)

Items 1 and 2 introduce new tunables (`idle_timeout_seconds`,
`max_session_seconds`, the balance-low threshold). Wiring those as more
global flags would deepen the very gap item 4 calls out, so the config
seam lands first.

**Current wiring.** `InterimDebitConfig`
(`payment.go:39-54`) is built once from CLI flags in
`capability-broker/cmd/livepeer-capability-broker/main.go:47-63,272-275`
and threaded as `s.opts.InterimDebit` into the single `Payment`
middleware instance (`internal/server/routes.go:44,57`). It is the same
for every offering.

**Seam.** The middleware already does a per-request
`lookup(capability, offering) (CapabilitySpec, bool)`
(`payment.go:214`, type at `payment.go:26-34`). Per-offering config
belongs in that lookup result, sourced from the capability's existing
`Extra map[string]any` (`internal/config/config.go:67`), which is
already validated per-capability (`internal/config/validate.go:496`,
`validateCapabilityExtra`).

**Design.**

1. Add a typed block read from `cap.Extra["session"]` (new key,
   sibling of the existing `openai` / `audio` / `video` blocks):

   ```yaml
   extra:
     session:
       debit_cadence_seconds: 5        # → InterimDebitConfig.Interval
       runway_min_units: 30            # → SufficientBalance threshold (terminate)
       balance_low_units: 90           # → item 2 warn threshold (> runway_min_units)
       idle_timeout_seconds: 60        # → item 1
       max_session_seconds: 14400      # → item 1 (0/absent = no cap)
   ```

2. Validate in `validateCapabilityExtra`: all non-negative;
   `balance_low_units >= runway_min_units` (a warn threshold below the
   terminate threshold can never fire); cadence below 1s rejected
   outside test config (mirrors the existing runtime WARN at
   `payment.go:193-196`).

3. Extend `CapabilitySpec` with an optional `SessionConfig` and have the
   broker's lookup populate it from `cap.Extra`, **falling back to the
   broker-global `InterimDebitConfig` flag values when a field is
   absent** (operator-global default, per-offering override — the same
   "operator wins, fall back to declared" contract the runner options
   endpoints already use).

4. The middleware uses the resolved per-request config to build the
   ticker (`payment.go:310-323`) and to populate the new `modes.Params`
   timeout fields (item 1). The CLI flags remain as the default source;
   no flags are removed.

**Why not thread config through dispatch instead?** The ticker is
constructed in the middleware *before* `dispatch` resolves the
capability, so dispatch is too late for the cadence/runway values.
Extending the lookup (which already runs in the middleware with the
capability in hand) is the minimal-surface fix.

## 4. Item 1 — idle & max-session timeouts

Entirely driver-side; no payment-layer involvement.

**Current.** `pumpFrames` (`driver.go:152-169`) calls
`src.ReadMessage()` with no deadline, so a silent peer blocks the
goroutine indefinitely; `Serve` has no overall session clock.

**Design.**

1. Thread two durations into the driver via new `modes.Params` fields
   (`internal/modes/types.go:35-54`), populated by dispatch from the
   resolved per-offering `SessionConfig` (item 3):
   `IdleTimeout time.Duration`, `MaxSessionDuration time.Duration`
   (zero = disabled, matching spec "optional cap").

2. **Idle timeout** via read deadlines refreshed per frame. In each
   `pumpFrames` loop, before `ReadMessage`, set
   `src.SetReadDeadline(now + idleTimeout)` (when > 0). A genuine idle
   expiry surfaces as a timeout error from `ReadMessage`; distinguish it
   from a peer close so the close frame (item 5) carries the right
   reason. Because gorilla read deadlines need a clock and the relay
   loop has no `time.Now` injection today, add a small `nowFn func()
   time.Time` to the driver (defaults to `time.Now`, overridable in
   tests) rather than calling `time.Now` inline — keeps the relay unit-
   testable.

3. **Max-session duration** via a parent timer. In `Serve`, when
   `MaxSessionDuration > 0`, derive `relayCtx` from a
   `context.WithTimeout(ctx, MaxSessionDuration)` instead of plain
   `WithCancel` (`driver.go:123`). On expiry the existing
   `ctx.Err()` check at the top of `pumpFrames` (`driver.go:155`) trips
   and both pumps exit; the close frame reports a session-duration
   reason.

4. Idle timeout is per-direction read but the spec says "no frame from
   *either* side" → close the whole session. The shared `cancel()`
   already collapses both pumps when one returns (`driver.go:153`), so a
   single side's idle expiry tears down the session, which satisfies the
   spec.

**Note on the overstated concurrency risk.** An earlier review flagged a
write-after-close panic in the dual-pump relay. That risk is low:
gorilla supports one concurrent reader and one concurrent writer per
connection, and `Close()` only fires via `defer` after `wg.Wait()`
(`driver.go:109,119,140`). The deadline work above does not change that
invariant. We will, however, add a race-detector run over the new
timeout tests (§8) since deadlines introduce a new `ReadMessage` exit
path concurrent with `cancel()`.

## 5. Item 2 — `Livepeer-Balance-Low` warning

Needs both layers: the ticker decides *when*, the driver sends the
*frame* (D1 channel).

**Current.** The ticker has a single threshold `minRunwayUnits`; on
`!res.Sufficient` it immediately terminates (`payment.go:538-575`).
There is no warn-before-terminate step and no socket access to emit a
warning.

**Design.**

1. Two thresholds (from item 3 config): `balance_low_units` (warn) and
   `runway_min_units` (terminate), with `balance_low_units >=
   runway_min_units` enforced at config-validation time. The ticker
   already calls `SufficientBalance` once per tick; add a second check
   at the warn threshold (or, preferably, a single `SufficientBalance`
   call returning remaining runway if the daemon RPC exposes it — see
   open question Q1; the v0.1 cut uses two calls to avoid a proto
   change).

2. On `sufficient(runway_min)=true` **and** `sufficient(balance_low)=false`
   (i.e. balance is in the warn band): `state.emit(SessionEvent{Kind:
   "balance-low"})` and `observability.IncSessionBalanceLow(mode,
   capability, offering)` (the metric the spec already names). Continue
   the session — do **not** cancel.

3. The driver, in `Serve`, runs a third goroutine selecting on
   `state.Events()`:
   - `balance-low` → write an application-level message to the **gateway
     side** (`inbound`). Spec §5 says the body is "capability-shaped";
     v0.1 sends a JSON text frame
     `{"type":"livepeer.balance_low","work_id":"…"}` and documents that
     capabilities MAY define a richer body later. The frame is *not*
     forwarded to the backend (spec: broker-originated control).
   - `terminate` → item 5 below.

4. Coalescing: re-emitting balance-low every tick would spam the gateway.
   The driver tracks "already warned" and only writes one balance-low
   frame per crossing into the band (resets if balance recovers above
   the band — observable because the ticker stops emitting).

## 6. Item 5 — reason-bearing close frames

Driver-side, consuming the D1 `terminate` event and the item-1 timeout
reasons.

**Current.**
- Backend dial failure → `CloseTryAgainLater` with the dial error string
  (`driver.go:113-117`). Acceptable, but the gateway can't distinguish
  it from other retryable states.
- Balance termination → `cancelHandler()` only (`payment.go:573`); the
  relay unblocks and both sockets close with gorilla's default close,
  carrying no `Livepeer-Error`. The gateway sees an abnormal closure
  with no cause.

**Design.** Centralise close-frame emission in the driver, keyed by the
termination cause:

| Cause | Source | Close code | `Livepeer-Error` reason |
|---|---|---|---|
| Backend dial failed | `driver.go:113` | `CloseTryAgainLater` (1013) | `backend_unavailable` |
| Insufficient balance | ticker `terminate` event | `ClosePolicyViolation` (1008) | `payment_invalid` |
| Idle timeout | item 1 read deadline | `CloseGoingAway` (1001) | `idle_timeout` |
| Max session duration | item 1 parent timeout | `CloseNormalClosure` (1000) | `max_session_exceeded` |
| Backend-initiated close | `out` read error | propagate backend close code/text | (none — relay it) |

Implementation:

1. Add a `closeWithReason(conn, code, livepeerErr, msg)` helper that
   writes a gorilla close control frame whose payload carries the
   `Livepeer-Error` token (per spec §5, the reason travels as the close
   message text in the agreed `code:reason` shape used elsewhere; align
   with `livepeerheader` token constants).

2. When `pumpFrames` exits, `Serve` inspects *why*: a `terminate` event
   (read reason from the event), an idle/max-session ctx error, or a
   peer close. It then sends the matching close frame to the **gateway**
   side before the `defer inbound.Close()` runs. For backend-initiated
   closes, propagate the backend's close frame verbatim to the gateway
   (spec §Forwarding "close frames forwarded both directions").

3. The ticker's `terminate` event carries the reason string it already
   computes (`payment.go:570 reason := livepeerheader.ErrInsufficientBalance`),
   so the driver maps it straight to `payment_invalid`. The existing
   post-handler WARN log (`payment.go:399-403`) stays for observability.

## 7. Item 3 — conformance & integration test realism

The existing fixtures only prove the path with `credit_ev=0`
(`interim-debit-balance-exhausted.yaml` always trips on the first tick,
per its own header comment, lines 10-15). We need green long sessions
and a real balance-low crossing.

**Design.**

1. **Funded long-session fixture** (`ws-realtime/interim-debit-funded.yaml`):
   requires the conformance harness to support a non-zero `credit_ev`
   stub (a fixed positive balance, no decrement) so `SufficientBalance`
   returns true for the whole run. Assert: ≥3 `DebitBalance` ticks, no
   broker-initiated termination, clean 1000 close, byte totals match.
   This is the first test that proves the happy long-running path at
   all. (Harness change tracked as a prerequisite — see Q2.)

2. **Balance-low crossing fixture**
   (`ws-realtime/interim-debit-balance-low.yaml`): a stub balance that
   starts in the green band and drops into `[runway_min, balance_low)`
   after N ticks (decrementing stub, or a scripted balance trajectory).
   Assert: exactly one `Livepeer-Balance-Low` frame delivered to the
   gateway, `session_balance_low_events_total` incremented, session
   continues (no termination), then a clean close. Extends the YAML
   `ws_realtime` block with `expect_balance_low_frames: 1`.

3. **Timeout fixtures**:
   - `ws-realtime/idle-timeout.yaml`: configure
     `extra.session.idle_timeout_seconds` small; runner upgrades, sends
     one frame, then holds silent past the timeout; assert broker-
     initiated close with `Livepeer-Error: idle_timeout`.
   - `ws-realtime/max-session.yaml`: small `max_session_seconds`;
     assert close with `max_session_exceeded` while frames still flowing.

4. **Reason propagation assertions**: extend the existing
   `expect_broker_terminated: true` block in
   `interim-debit-balance-exhausted.yaml` with
   `expect_close_reason: payment_invalid` so item 5 is regression-locked.

5. **Driver unit tests** (new — there are none today): table-driven tests
   over `pumpFrames` and the close-reason helper using in-process gorilla
   server/client pairs and the injectable `nowFn`. Cover: idle deadline
   fires; max-session ctx expiry; balance-low event → exactly one frame;
   terminate event → correct close code+reason; backend close
   propagation. Run under `-race`.

6. **Real-daemon integration**: a single end-to-end test in the broker
   integration suite that runs against an actual payment-daemon instance
   (not the conformance stub) for one funded session + one exhaustion,
   to catch daemon-integration drift the stub hides. Scope it to smoke,
   not exhaustive matrix.

## 8. Implementation phases

Ordered by dependency; each phase is independently mergeable and keeps
broker smoke + conformance compose green.

1. **Config seam (item 4).** `extra.session` parsing + validation,
   `CapabilitySpec.SessionConfig`, lookup populates it with flag
   fallback. No behaviour change yet (values unused). Add config
   round-trip unit tests.
2. **Control channel (D1).** `SessionState` events API + ticker `emit`
   on terminate (mirrors existing cancel). Driver consumes `terminate`
   to send the `payment_invalid` close frame (item 5, balance path).
   New driver unit tests for close reasons.
3. **Timeouts (item 1).** `modes.Params` fields, `nowFn`, idle read
   deadlines, max-session parent timeout, their close reasons. Driver
   unit tests + idle/max-session conformance fixtures.
4. **Balance-low (item 2).** Second threshold in the ticker, balance-low
   event + metric, driver emits the warn frame with coalescing.
   Balance-low fixture.
5. **Test realism (item 3 remainder).** Funded long-session fixture
   (after the harness `credit_ev` prerequisite lands), real-daemon
   integration smoke, reason-propagation assertions on the existing
   fixture.

## 9. Open questions

- **Q1.** Does `PayeeDaemon.SufficientBalance` (or a sibling) expose
  *remaining runway* so the ticker can derive both warn and terminate
  bands from one RPC, instead of two `SufficientBalance` calls per tick?
  If not, v0.1 ships two calls and we file a proto follow-up.
- **Q2.** The conformance harness currently hard-codes the `credit_ev=0`
  stub. A funded/decrementing-balance stub is a prerequisite for the
  item-3 green-path and balance-low fixtures. Is that a harness change
  this plan owns, or a separate conformance-infra task?
- **Q3.** Balance-low frame body: v0.1 sends a fixed
  `{"type":"livepeer.balance_low"}` JSON text frame. Do any current
  capabilities need a richer/capability-shaped body now, or is the
  generic shape sufficient until a consumer asks?
- **Q4.** Close-frame reason encoding: confirm the exact on-wire shape
  the gateway adapter expects for `Livepeer-Error` inside a WS close
  frame (close text payload vs. a preceding control message), so item 5
  and the openai-gateway `realtime` adapter agree.

## 10. Risks

- Items 1, 2, 5 all add code to the relay/teardown path, which is the
  most concurrency-sensitive part of the driver. Mitigation: every new
  exit path gets a `-race` unit test (§7.5) before the conformance
  fixtures.
- Per-offering config (item 4) widens the config schema; a malformed
  `extra.session` block must fail validation loudly at load, not
  silently fall back, to avoid an operator thinking a timeout is set
  when it isn't. Validation (§3.2) is mandatory, not advisory.
- The balance-low warn band depends on the daemon's balance accounting
  being meaningful; against the `credit_ev=0` stub the band never
  applies, which is exactly why item 3's funded stub is a prerequisite,
  not optional.
