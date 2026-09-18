# Clearinghouse integration packet — LOC → v1 protocols

Date: 2026-08-19. For the team behind Livepeer Open Clearinghouse
(`basic-pymnthouse`). Based on a read of the codebase on 2026-08-18.

## The headline

LOC hard-codes the interaction-mode taxonomy in two places:

```python
JOB_MODES = {http-reqresp@v0, http-stream@v0, http-multipart@v0}
SESSION_OPEN_MODES = {ws-realtime@v0, session-control-plus-media@v0,
                      rtmp-ingress-hls-egress@v0, live-session-remote-runner@v0,
                      live-session-gateway-ingest@v0}
```

Those two sets are the *only* thing LOC needed from the taxonomy — and they
are exactly the distinction the v1 rebuild kept. Everything else the mode
names encoded (transport shape, media-plane location, who meters) collapsed
into per-offering declared axes.

**After migration, both lists become two protocol names**, and they stop
growing. A new capability with a new runtime shape — a meeting SFU, a
generative runtime, whatever ships next — requires **zero changes in LOC or
its SDKs**. Today each one costs you a set-membership edit, a release, and
an SDK that knows a new mode's top-up channel.

## The mapping

| LOC today | v1 |
|---|---|
| `route.extra["interaction_mode"]`, hard error at session open when missing | `protocol` — a **required, first-class manifest field** (`<name>/v<major>`), not opaque `extra` metadata. Still passed through by registry and coordinator untouched. |
| `JOB_MODES` set membership | `protocol == "paid-job/v1"` |
| `SESSION_OPEN_MODES` set membership | `protocol == "paid-session/v1"` |
| Refillability as per-mode policy (`ws-realtime@v0` → `refill_not_supported_for_mode`) | `session.refill: extensible \| bounded`, declared per offering. Same refusal, sourced from a declaration instead of a hard-coded exception. |
| SDKs knowing each mode's top-up delivery channel (`session.topup` control-WS frame vs `POST {control.topup_url}`) | **One** session control contract. `topup_url` is always present in the open response's `control` object; the control-WS is an *optional* push mirror advertised as `control.events_ws`. The HTTP verb is authoritative for every session, so the SDK needs one path, not a per-mode branch. |
| Mode discriminated by offering name in some catalogs | Not needed — transports are declared (`job.transports`), so one offering serves unary + stream + multipart. Expect the per-mode offering duplication in partner catalogs to disappear. |

Two axes are worth surfacing in your catalog even though you don't gate on
them, because customers do: `session.descriptor_schema` (a gateway must not
open a session whose runtime shape it can't consume — this is the thing
that actually determines compatibility now) and `job.transports`.

## What stays exactly as it is

> **Correction (2026-08-20).** Two claims in this section were wrong when
> written. `work_id` was NOT untouched: `paid-job/v1` derived it from the
> payment's `recipient_rand_hash` as described, but `paid-session/v1`
> minted a UUID, so in chain mode no session payment could validate. And
> `GetSessionDebits` keys on nothing — the payer daemon never implemented
> it and holds no debit ledger. Both are fixed; see
> `2026-08-20-loc-review-reply.md` and the replies that follow it. The
> section is left as sent, with this note, because a packet that quietly
> rewrites itself is worse than one that carries its errata.

- **`work_id`** — the daemon-issued `recipient_rand_hash`, your public
  lookup key. (See the correction above: this was true of jobs only until
  `87ef866` made it true of sessions too.)
- **The handoff-mode boundary.** LOC remains a control plane; the envelope
  still travels to the broker in the customer's own request. The v1 work
  does not put anything new in your data path, and your blast-radius
  argument from exec-plan 002 still holds.
- **`Livepeer-Settlement`** — unchanged shape and meaning.
- **Charge-EV-at-issuance.** Nothing here touches your pricing model.

## What changes on the wire your SDKs speak

| Header | Change | SDK impact |
|---|---|---|
| `Livepeer-Mode` | **Removed** | Replaced by `Livepeer-Protocol` |
| `Livepeer-Spec-Version` | **Removed** | The protocol tag carries its version; drop the constant |
| `Livepeer-Request-Id` | Now **required**, and is the idempotency key | See below — this is the good one |
| `Livepeer-Work-Units` | Now on **every** terminal job response including errors (`0`), trailer-delivered on streams | Your settle path gets a number it can trust |
| `Livepeer-Work-Unit` | **New** — echoes the declared unit name | Cheap client-side drift check |
| `Livepeer-Job-Id` | **New** — broker's audit key for the exchange | Worth storing next to your `job_id` |
| `Livepeer-Balance-Low` | **Removed from jobs** | See "one deliberate removal" below |

### Idempotent opens change your failure handling

`Livepeer-Request-Id` now has normative replay semantics on both protocols:
a retried request with identical content converges on the recorded outcome —
same status, same claim, same job id, no second backend execution, **no
second debit**. In-flight retries get `job_in_flight`; reuse with different
content gets `request_id_reuse`.

This is the fix for a pattern both surveyed gateways built independently:
because LOC's own creates are not idempotent, `doJSONRetry` is banned on
open paths and every failure branch must hand-roll a compensating
`settle(0)` / `close(0)` to release encumbrance. The broker side of that is
now solved. **The LOC side is not** — your `POST /v1/jobs` and
`POST /v1/sessions` remain non-idempotent, and that is now the weaker half
of the chain. Worth considering the same key.

### One deliberate removal: no balance signaling on jobs

`Livepeer-Balance-Low` is gone from `paid-job/v1`. One envelope funds one
exchange, settled once; there is no mid-job refill verb, so a mid-flight
warning had nothing actionable behind it. Long-running streams are protected
seller-side by a funded ceiling instead (the broker may end a stream at the
envelope's limit, claiming exactly the delivered units).

Runway signaling now lives entirely in `paid-session/v1`, where it's real —
and it's better than what you had.

### The balance object is now normative

Your session reconciliation (and the transcode gateway's top-up loop) reads
a `balance` object that was **undocumented** — it existed because the broker
happened to emit it. It's now spec'd:

```json
{ "status": "ok|low|exhausted", "claimed_units": 1284, "debited_units": 1284,
  "unit": "participant_minutes", "runway_units": 716,
  "runway_seconds_estimate": 430, "will_refuse_next_refill": false }
```

`will_refuse_next_refill` is a one-refill-ahead winddown warning with teeth:
the broker must advertise it *before* refusing a refill, and must never
accept a top-up it won't honor with lease extension. That gives your refill
scheduler a clean drain signal instead of discovering refusal mid-session.

## Two things we found in your code

Reporting these because they matter to the joint picture, not to pile on:

1. **`get_session_debits(sender=b"")`.** Both call sites pass an empty
   sender rather than the pooled wallet address. The daemon ledger is your
   trust anchor for money — if this is masking to "any sender," it's worth a
   look before it masks something real.
   *(Correction, 2026-08-20: the empty sender was never the binding
   constraint — the payer daemon does not implement this RPC at all. The
   reconciliation source is the payee-side signed settlement record.)*
2. **A naming collision worth documenting.** Your
   `payment_session.last_debit_seq` is a *refill* counter (surfaced as
   `refill_seq` / `refill_count`). The broker's `debit_seq` is a different
   thing: the payee-side `(sender, work_id, debit_seq)` idempotency key for
   individual debits. Same name, different scopes, both money-adjacent. We
   made the broker's semantics explicit in `paid-session/v1` §9.1; a rename
   or a doc note on your side would prevent someone conflating them during
   an incident.

## The joint issue: recipient rotation

This one is ours-and-yours, and neither the v1 protocols nor LOC currently
solve it.

`INVALID_RECIPIENT_RAND` today has no recovery path: LOC refills are pinned
to the original recipient, `ReportPaymentResult` isn't in your
`PaymentDaemonClient` protocol at all (rotation is handled reactively by
mapping gRPC `ABORTED` → `InvalidRecipientRand`), and there is no
`force_rotate`. Downstream, the transcode gateway carries a bespoke
retry-once-then-give-up in three places, and mid-session the terminal
outcome is `rotation_unrecoverable` — **which kills a live broadcast**.

The v1 protocols make the broker side well-behaved (idempotent opens,
exactly-once debit, a spec'd refusal path), but a session whose payer can't
rotate its recipient still dies. Fixing it means a rotation handshake at the
payer/payee seam — realistically `ReportPaymentResult` plus a way to
re-establish a session against a rotated recipient without minting a new
logical session.

We'd like to design that together rather than each side working around it.
It is the highest-value remaining item on the payment path, and it is
squarely at the boundary between your daemon usage and our broker's session
authority.

## What we need from you

1. **Confirm the two-protocol gate is sufficient** for your open paths — if
   there is any decision LOC makes today that the declared axes can't
   express, we want to know before this lands, not after.
2. **A view on idempotency keys for `POST /v1/jobs` / `/v1/sessions`** (see
   above).
3. **A working session on recipient rotation** — name someone and we'll
   bring the broker/payee side of the design.
