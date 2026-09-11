# Recipient rotation — design proposal

Date: 2026-08-20. Status: **proposal**, for agreement with the LOC team
before implementation. Written against `806f6bb` on `tasks/lpm-v2`.

Modules drives this because the seam is ours: the payer daemon, the payee
daemon and the broker all have to move together, and LOC only has to read
what comes out.

## The problem, stated narrowly

A payee's recipient rand is the anchor of a payment session. Tickets are
signed against it, `work_id` is its hash, and the payee validates every
ticket by recomputing that hash. When the rand changes under a live
payer, every subsequent ticket is rejected with
`INVALID_RECIPIENT_RAND`.

For a one-shot job this is already solved and needs nothing: the payer
evicts its cached session, the retry fetches fresh params, mints against
the new rand and the job proceeds. One extra round trip, no state to
carry.

For a **live `paid-session/v1` session** it is unsolved. The broker binds
a session to one `work_id` for its entire life. There is no verb to move
an open session to a new payment identity, so a rotation mid-session
strands it: the runner is serving, the lease is ticking, and no top-up
can ever be accepted again.

## What already exists

Worth naming, because the proposal deliberately adds as little as
possible on top of it.

| Piece | Where | What it does |
|---|---|---|
| Rejection reporting | payee `ProcessPayment` | Reports `INVALID_RECIPIENT_RAND` / `NONCE_CAP_REACHED` per ticket, with a dominant reason for the batch |
| Payer eviction | `ReportPaymentResult` (`sender.go:302`) | Drops the cached session for that `work_id`, returns `Aborted` + `ErrorInfo{INVALID_RECIPIENT_RAND, old_work_id}` + `RetryInfo{0}` |
| Payee reset | `ResetSession` (`receiver.go:512`) | Rotates the rand for a stable (sender, recipient, capability, offering); returns the old `work_id`. Admin verb — nothing calls it today |
| Params re-fetch | broker `POST /v1/payment/ticket-params` → payee `GetTicketParams` | Issues the current rand for the stable identity; a closed session yields a fresh one |
| Legible failure | broker `sessionengine` (as of `87ef866`) | An all-rejected batch fails closed and names `INVALID_RECIPIENT_RAND` instead of opening an unfunded session |

So the payer half of the loop is complete. **The gap is entirely on the
broker's side of a live session**, plus the accounting that has to
survive it.

## Decisions

### 1. Who authorizes rotation

**The payee, always.** The rand is the payee's secret; rotation is the
payee replacing it, and the payer only ever *reacts*. A payer cannot
request rotation, and a broker cannot rotate on a gateway's say-so.

This matters beyond tidiness: if a payer could force rotation it could
force the nonce watermark to reset, and the replay protection that
watermark provides would be a payer-controlled property.

Rotation triggers, all payee-side:

- the payee's ticket session was closed and re-issued (restart with lost
  state, operator `ResetSession`, redemption lifecycle);
- `NONCE_CAP_REACHED` — the nonce space for one rand is exhausted. Same
  remedy, different cause, and it is the one that happens on a *healthy*
  long session rather than a failure.

### 2. How the new identity reaches an open session

**On the top-up that already has to happen — with the rebind declared,
not inferred.**

A gateway whose top-up was rejected re-fetches params (existing path),
mints against the new rand (existing path), and retries the top-up with a
**new** `Livepeer-Request-Id` — a rotation retry is a new intent, the
same rule the mint id already follows. The retry additionally carries
`rebind_from: <old_work_id>`: one optional field, set only on this
branch.

We considered an explicit `POST /v1/session/{id}/rebind` and rejected it.
The top-up already carries the evidence a rebind needs — a valid payment
against the new identity — so a separate verb would add a state machine
to every gateway SDK for an event most will never see.

We also rejected pure inference, where the broker treats any mismatched
`work_id` as a rotation. Inference at a money boundary is where
brittleness lives: a gateway that retries session A's top-up with session
B's freshly minted payment — an ordinary concurrency bug — would have its
mistake silently absorbed as an identity change. Declaring the intent
costs one field and turns that into a clean refusal.

**The three guards, and the one that does the real work:**

1. `rebind_from` MUST equal the session's current `work_id`;
2. `ProcessPayment` against the new `work_id` MUST credit — not an
   all-rejected batch;
3. the sealed sender MUST match the session's.

(2) is the load-bearing one. **Tickets minted against a fake or stale
identity cannot validate**, so a successful credit is proof the successor
is genuine — no assertion by the gateway is trusted, and no rotation
bookkeeping has to survive a broker restart for the check to work.

An earlier draft required the old `work_id` to be in a rotated state
"confirmed by its own dominant rejection". That is dropped: it is
circumstantial, and it fails a rebind whose rejection predates a restart.

Note what the broker MUST NOT do to verify: calling the payee's
`GetTicketParams` for the stable tuple **mints a fresh rand when no
ticket session is open**, so the verification step would itself cause a
rotation.

Rebinding is refused on a terminal or winding-down session, like any
top-up.

The rebind is recorded, not silent:

- `rotation_generation` and the current `work_id` appear in session
  status;
- a `session.rebound` **control-plane** message is emitted to the gateway
  (WS, and a status transition for pollers). It is infrastructure
  signalling between broker and gateway, not a session lifecycle event —
  see §6;
- the settlement record carries the generation chain (§4).

### 3. Retry limits and terminal behaviour

Bounded, and the bound is the interesting part: an unbounded rotate-mint
loop burns the payer's deposit without ever delivering work.

- At most `session.max_rotations` per session (default **3**,
  operator-configurable per offering).
- Exceeding it ends the session with `close_reason:
  payment_unrecoverable` — the consequence, not the mechanism. Which
  rotation attempt failed, and how many there were, belongs in the
  settlement chain and the operator's logs (§6).
- Each rotation must be separated by at least one accepted payment;
  two consecutive rotations with no accepted payment between them counts
  as exhausted immediately, because that is a rotation loop rather than a
  session that keeps being unlucky.

### 4. Exactly-once accounting across the seam

The rule agreed with LOC, restated with what it implies for us:

- **Cumulative claimed/debited units never reset.** They live on the
  broker's session record and span every generation.
- **Funding is per generation.** Each `work_id` has its own payee balance
  and its own `debit_seq` space; `debit_seq` restarting at a rebind is
  correct, since daemon idempotency is keyed `(sender, work_id,
  debit_seq)`.
- **The old generation is settled before it is closed.** Any
  claimed-but-undebited units are debited against the OLD `work_id`
  first. Only then does the broker close it. A rebind that cannot settle
  the old generation does not proceed — it winds the session down
  instead, because carrying unsettled work across an identity change
  would make the ledger unauditable.
- Billing stays a single ceiling over cumulative units
  (`offering-axes.md` §6.1), never a sum of per-generation ceilings.

### 5. Credential and session continuity

Unchanged across rotation, by construction:

- the session credential (`sc_…`) is **not** re-issued — the gateway
  keeps using the one it has;
- grants are **not** re-minted (`paid-session` §9.2 already forbids
  re-emitting them);
- `session_id` is stable — this is the second reason it survived the
  identity decision;
- the runner is never told, because nothing it holds depends on the
  payment identity.

So rotation is invisible to the runner and nearly invisible to the
gateway: one refused top-up, one re-fetch, one retry.

### 6. What a rotation is visible as (settled with LOC, 2026-08-20)

**A completed rotation is settlement-only.** It is infrastructure
recovery the customer neither caused nor can act on, so it produces no
customer-facing lifecycle event.

It MUST appear in the signed settlement/audit chain, carrying:

- the stable `session_id`;
- `rotation_generation`;
- `predecessor_work_id` and the current `work_id`;
- cumulative accounting continuity across the generations.

The `session.rebound` message in §2 is explicitly **not** a customer
lifecycle event: it is an internal control-plane message that completes
the handshake between broker and gateway. A gateway MUST NOT surface it
as session history.

**Failure is visible; the mechanism is not.** When rotation fails,
exhausts its bound, or interrupts the workload, the customer sees the
resulting degraded or terminal session state — `payment_unrecoverable`,
with the usual balance and lease semantics — and not the fact that a
recipient rand rotated underneath it. The rotation detail stays in
settlement and operator telemetry, where the people who can act on it
are.

## What this deliberately does not solve

**Stranded balance on the old generation.** Credited-but-unspent EV on a
rotated-away `work_id` is lost to the payer. Balances are per payee
session and there is no transfer primitive; inventing one would mean
moving credited value between sessions, which is exactly the kind of
mechanism that turns an auditable ledger into a puzzle.

The mitigation is sizing, not machinery: fund a session in increments you
are willing to strand, and note that rotation is rare and bounded (§3).
We would rather state this cost plainly than hide it behind a transfer
verb nobody can reason about.

## What each party implements

**Modules (us):**

| Work | Where |
|---|---|
| Rebind path on top-up: detect, guard, settle-old, open-new, generation++ | `capability-broker/internal/sessionengine` |
| `rotation_generation`, `predecessor_work_id` on the session record and in status | broker |
| `session.rebound` control-plane message (not a lifecycle event) | broker |
| `session.max_rotations` axis, the consecutive-rotation rule, `payment_unrecoverable` | broker config + `offering-axes.md` |
| Settlement payload fields and per-generation subtotals | broker + `lnm-sqe.13` |
| A distinct wire error code so a gateway does not string-match a message | `headers/livepeer-headers.md` |
| §3.3/§9 spec text for all of the above | `paid-session.md` |

**LOC:** reads `session_id`, `rotation_generation`,
`predecessor_work_id`, the current `work_id` and the per-generation
subtotals from the signed settlement chain; treats a rotation as one
logical session for billing, and surfaces nothing to the customer unless
the session degrades or terminates. No new call, no new verb.

**Gateways:** retry a refused top-up with fresh params, a fresh request
id, and `rebind_from` set to the session's previous `work_id`. Everything
but that last field is a correct implementation of the existing top-up
contract. The other new behaviour is *not* treating a rotation refusal as
fatal, and not surfacing `session.rebound` as customer-visible history.

## Next

On agreement this becomes spec text plus implementation beads under
`lnm-sqe.10`. It should land **before** `lnm-sqe.13` (settlement
signing): the signed payload has to carry the generation chain, and
signing a shape we then change is wasted work.
