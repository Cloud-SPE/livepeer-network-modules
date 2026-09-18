# Reply to the LOC review — the seven contract points

Date: 2026-08-20. Response to the LOC team's second reply on the
2026-08-19 integration packet. Written against the code at `933935b`
on `tasks/lpm-v2`.

## Short answer

You were right on every point that could be checked against code. Six of
your seven questions turned up defects in **our** implementation, not
ambiguities in the packet, and one of the six is worse than you described
it. The seventh — recipient rotation — is the joint gap we already agreed
on.

Nothing below needs a compatibility layer. All of it is us fixing code
and writing invariants down. It is tracked as epic `lnm-sqe` (12
children) so you can hold us to specific items rather than to this
document.

**One correction to the packet first.** It said `work_id` is "still the
daemon-returned `recipient_rand_hash` … Untouched." That is true for
`paid-job/v1` and false for `paid-session/v1`. You caught it; the rest of
this section explains how far the crack goes.

## 1. Authoritative session identity

**What we found.** Jobs derive the payee-side `work_id` from the payment
itself — `hex(ticket_params.recipient_rand_hash)`, in
`capability-broker/internal/server/middleware/workid.go:17`. Sessions do
not: `internal/sessionengine/engine.go:229` mints
`workID := uuid.NewString()`.

That is not merely two spellings of an id. The payee daemon's
`OpenSession` mints a **fresh recipient rand** for a work_id it has not
seen (`payment-daemon/internal/service/receiver/receiver.go:115-129`),
while the sender's tickets were minted against the rand issued by
`GetTicketParams` (`receiver.go:566`, where `work_id = hex(rrHash)`). The
job path works precisely *because* its work_id collides with the ticket
session, so the store reuses that session's rand. A session-open work_id
never collides, so in chain mode the tickets in a session-open payment
cannot validate against the session they are being credited to.

It gets past our tests because the open path then ignores the rejection —
`engine.go:256` reads only `payRes.Sender` and drops `TicketsRejected` /
`DominantRejection` / `Balance` — and because conformance runs against a
mock that credits without validating (`internal/payment/mock.go:55`). The
job middleware does check the rejection
(`internal/server/middleware/payment.go:268`). Sessions do not.

**Our decision, and it matches your preference.**
`recipient_rand_hash` is authoritative on both protocols. The
`sess_<uuid>` value survives but is demoted to what it actually is: a
local resource id for `/v1/session/{id}` routing, never a payment key,
never a billing identity. LOC stores `hex(rrHash)` as the authoritative
`work_id` for jobs and sessions alike, and may keep the `sess_` id
alongside it as an opaque route handle if that is convenient.

We are **not** taking the separate opaque payer-session-handle option you
offered as an alternative. This seam already carries one namespace too
many; adding a third to paper over the second is the wrong direction.

Tracked: `lnm-sqe.1` (P0), `lnm-sqe.2` (P1).

## 2. Refill identity

**What we found.** Your suspicion about the payer cache is correct in
form and — as it happens — harmless in effect, for a reason nobody wrote
down.

The payer's session key does include the funded value
(`payment-daemon/internal/service/sender/sender.go:530`). But the payee
pins the rand to the stable identity `(sender, recipient, capability,
offering)` for as long as the ticket session is open
(`receiver.go:536-546`), so a re-fetch at a different funded value comes
back with **the same** `recipient_rand_hash`, and therefore the same
work_id. Refill sizing does not change session identity today. It costs a
redundant ticket-params round trip, and it leaves the invariant resting
entirely on the payee's behaviour while the payer's cache key implies the
opposite.

**The invariant we will write into the payments spec**, which is what you
asked for:

- Refill sizing never changes session identity.
- Face value is pinned at first issuance for the life of the session.
- A larger refill mints **more** tickets, not larger ones.

And we will drop funded value from the payer's session key so the code
stops implying otherwise.

Tracked: `lnm-sqe.8`.

## 3. Registry projection

**What we found.** Agreed that we should not prescribe your
architecture, and the collision rule is not an architecture question —
it is a correctness one, and today it is backwards.

`service-registry-daemon/internal/types/coordinator_envelope.go:327`
(`mirrorDeclaration`) copies the signed tuple's `protocol`, `job` and
`session` into the projected capability's opaque `extra` block **only if
no key of that name is already there**. Operator-supplied `extra` wins.
That block is what becomes `extra_json` on the wire
(`internal/runtime/grpc/convert.go:216`) and it is what you and the
gateways gate on. An orch publishing `extra.protocol` can make a
`paid-session/v1` offering read as `paid-job/v1` downstream.

**Fix:** the signed tuple field is authoritative and overwrites, or the
publish is rejected on collision. We will implement one of the two, with
a test, and we lean toward rejecting at publish — a silent overwrite of
operator metadata is its own surprise.

**On typed fields:** we intend to add typed `protocol` to
`SelectedRoute`, because it is the field everyone gates on and it should
not travel as opaque bytes. We intend to keep the axes objects as
pass-through bytes, for the reason the coordinator already documents: a
typed mirror silently drops any axis a later spec minor adds, and the
drop gets baked into the operator's signature. Pass-through cannot go
stale. Either way, `extra_json` will not be able to carry a colliding
key.

Tracked: `lnm-sqe.4`.

## 4. Pricing denominator

**What we found.** Your concern is not hypothetical — `per_units` is
discarded in transit today, and the two ends disagree about it.

- The broker advertises `per_units`
  (`capability-broker/internal/server/registry/offerings.go:56`) and its
  config requires `per_units > 0`
  (`internal/config/validate.go:308`).
- The coordinator parses it into `BrokerOffering.PerUnits`
  (`orch-coordinator/internal/types/types.go:40`) and never uses it.
- The manifest schema has `price_per_unit_wei` and no `per_units`
  (`livepeer-network-protocol/manifest/schema.json:164`), so the signed
  document cannot express it.
- The resolver hard-codes `UnitsPerPrice: 1`
  (`service-registry-daemon/internal/runtime/grpc/convert.go:243`).
- Broker settlement, meanwhile, divides billed value by the
  `units_per_price` carried in the *payment*
  (`internal/server/middleware/settlement.go:80-84`).

So an operator pricing at `per_units: N` publishes a quote at N times the
rate the broker actually bills. Exactly the "correct charging becomes
impossible" case you named.

**Two ways to close it, and we want your preference before we implement:**

1. **Carry it.** `per_units` becomes a typed field on the manifest tuple,
   the coordinator envelope and `SelectedRoute`. Preserves pricing below
   1 wei per unit, which matters for token-metered offerings since
   `price_per_unit_wei` is an integer decimal string.
2. **Pin it.** v2 requires `per_units == 1`; the broker rejects anything
   else and stops advertising the field. Simpler, and costs the
   sub-wei-per-unit case.

We lean toward (1) because it keeps cheap-unit pricing expressible and
because the payment wire already carries the denominator, so pinning
would leave a field on the wire that the catalog is forbidden to use. But
you do the charging, so if (2) is easier for LOC and its SDKs, say so —
this is a decision worth making once, now, while the manifest schema is
still cheap to change.

Tracked: `lnm-sqe.3`.

## 5. Broker idempotency

All three of the behaviours you named are real. We are not going to
defend the gap between the advertised guarantee and the implementation.

- **Body-length-only job fingerprints.** Confirmed:
  `internal/server/job_routes.go:145` hashes
  `capability|offering|payment|ContentLength`. A retry that reuses the
  request id and envelope but changes the body to one of equal length
  gets the first exchange's recorded outcome instead of
  `request_id_reuse`. (`lnm-sqe.6`)
- **Non-fingerprinted session opens.** Confirmed:
  `internal/sessionengine/engine.go:224` replays on the request id alone,
  with none of the fingerprint comparison the job path does.
  (`lnm-sqe.7`)
- **Top-ups that ignore request ids.** Confirmed:
  `internal/server/session_routes.go:245` reads only the payment header.
  With identical bytes the daemon's nonce-replay protection absorbs a
  retry; with a freshly minted payment — which is what an SDK retry
  actually sends — the customer funds twice. (`lnm-sqe.5`)

Your composition model is the right one and we accept it: each hop owns
its own fingerprint and replay outcome, a correlation id ties them
together, and no single key implies a shared fingerprint across hops.
When we spec each mutating hop we will state fingerprint inputs, durable
commit point, in-flight response, mismatch response, retention period and
replayable outcome, as you proposed.

**One question back, from your round-1 note.** You asked that session-open
replay recover usable credentials after a lost response. Today it
deliberately does not: secrets are delivered exactly once, and a replay
returns the session without credential or grants. The consequence is a
gateway that loses the open response holds a funded session it cannot
drive, with no verb to release the funds before the lease expires. Two
ways out — re-deliver to the same authenticated caller on replay, or add
an abandon-and-settle path — and they trade secret hygiene against
recovery. We have opened `lnm-sqe.12` for the decision and would rather
make it with your input than hand you the outcome.

## 6. Recipient rotation

Agreed on all counts; this is the genuine live-session gap, not a
compatibility item.

What exists today: the payer detects the payee's `INVALID_RECIPIENT_RAND`
via `ReportPaymentResult`, evicts the cached session and returns `Aborted`
with a retry-exactly-once hint
(`payment-daemon/internal/service/sender/sender.go:200-218`); the payee
can reset a session and report the old work_id. That composition is
sufficient for a one-shot job, because the retry simply mints a new
identity.

It is not sufficient for a live session, and the reason is structural:
the broker binds a session to one work_id for its entire lifetime and has
no verb to rebind it. Rotation on a live session currently has nowhere to
land — and per §1 above, the session open path does not even surface the
rejection that would trigger it.

We will bring to the working session: who authorizes and initiates
rotation, how the new recipient reaches an open session, retry limits and
terminal behaviour, exactly-once accounting across the seam, and
credential/session continuity through the handshake — your list, which we
think is the right list.

Tracked: `lnm-sqe.10`.

## 7. Debit reconciliation

**The empty sender was never the binding constraint.** The payer daemon
does not implement `GetSessionDebits` at all — the service embeds
`pb.UnimplementedPayerDaemonServer`
(`payment-daemon/internal/service/sender/sender.go:42`), so the RPC
returns `UNIMPLEMENTED` regardless of what sender you pass. It also
exposes no wallet address: `GetDepositInfo` returns deposit, reserve and
withdraw round only. So LOC could not supply a correct sender even if it
wanted to, and would get nothing back if it did. Fixing your call sites
alone would have produced the same zeros with better arguments.

**Our proposal:** reconciliation reads the payee side, not the payer.
The authoritative sources are the session `balance` object
(`claimed_units`, `debited_units`, `unit` — now normative) and the
`Livepeer-Settlement` record at close. One caveat you should have
explicitly: that record is **not signed** today. It is a broker
attestation delivered over an authenticated channel, which is enough for
reconciliation and not enough for a dispute you expect to win against a
hostile counterparty. If LOC needs non-repudiability there, tell us and
we will sign it — that is a contained change, and better made before your
SDK work than after.

If you would rather the payer daemon become the source, that is a real
build: an identity RPC plus an actual debit ledger on the sender side.
We do not recommend it — the payee already has the ledger, and two
ledgers is how they drift — but the choice is yours to weigh.

Tracked: `lnm-sqe.9`.

## What we need from you

1. **`per_units`: carry or pin** (§4). The one decision that changes what
   your charging code does.
2. **Does `Livepeer-Settlement` need to be signed** for your dispute path
   (§7)?
3. **Session-open replay after a lost response** (§5 tail): re-deliver
   credentials, or abandon-and-settle?
4. **Rotation working session** (§6) — we are ready; scheduling and our
   participant are being confirmed separately.

## What we are not doing

- No v0 compatibility layer or dual stack. Confirmed, and we are glad it
  is mutual.
- No third session-identity namespace (§1).
- No second debit ledger on the payer side unless you ask for it (§7).

## Status of this reply

Every claim above is anchored to a file and line at `933935b` on
`tasks/lpm-v2`. The twelve items are open beads under `lnm-sqe`; the P0
is the session work_id defect, because until it lands no chain-mode paid
session can validate a real ticket.
