# To LOC — both requests landed, and one of them was a live bug

Date: 2026-08-21. Branch `tasks/lpm-v2`, head `2c86707`.

Thanks for the acceptance. Both remaining requests are implemented.

## 1. Paid-job binding — `request_id` in the signed settlement

Agreed, and this answers the question we left open in the last note (we
had asked whether the job path needed it; you have now said yes, and the
reasoning holds).

`SettlementRecord` gains `request_id` (field 27), inside the signature,
carrying the exchange's `Livepeer-Request-Id`. It is the job path's
counterpart to `gateway_session_id`: `work_id` is the ticket session's
rand hash that every job on the session shares, and `job_id` narrows it
to one exchange but is broker-minted and reaches you only through the
customer's SDK.

One behaviour worth knowing: a broker generates a request id when the
caller sends none, and echoes it either way, so the field is always
populated. It binds to *your* durable job only when the gateway chose the
id. If you need that binding guaranteed, require your gateways to send
`Livepeer-Request-Id` — we did not make broker-generated ids
distinguishable in the record, and we can if you want that.

Normative in `paid-job` 1.0.5-draft §5.1.

## 2. Session settlement lookup — and what we found doing it

`GET /v1/settlement/{id}` now resolves `gateway_session_id`. Lookup order
is job id → `session_id` → `gateway_session_id` → `work_id`.

Your instinct that a `work_id` lookup "can return the wrong valid record"
was not a theoretical concern about our design. It was a live bug: the
work_id lookup scanned every session and kept the **last** match, so with
several sessions on one ticket session it returned whichever session id
sorted last in the index. Correctly signed, wrong session, no way to tell
from the record. Thank you for the report — it was precise enough to find
the code.

Three changes, because a lookup is only worth having if it resolves to
one session:

- **`gateway_session_id` is now unique across retained sessions.** A
  colliding open is refused with `gateway_session_id_reuse` (409). We
  refuse rather than accept because a duplicate breaks the lookup for the
  *earlier* session as well as the new one. It also may not equal a
  broker `session_id`, since that key is tried first.
- **An ambiguous key fails instead of guessing.** A `work_id` matching
  several sessions answers `ambiguous_identifier` (409) and names a key
  that resolves. You told us you can reject a wrong record but cannot
  find the right one — this gives you the second half.
- **Eviction releases the id**, so a gateway that reuses ids across days
  is not refused an open on account of a session that no longer exists.

Normative in `paid-session` 1.0.8-draft §3.3.1;
`livepeer-headers` 1.0.5-draft carries both error codes and the lookup
keys.

### One thing to check on your side

Uniqueness is enforced globally, not per-payer. If your gateways derive
`gateway_session_id` from something that could collide across tenants —
a per-tenant counter, a short slug — two of them will now race and the
loser gets a 409 at open. UUIDs are fine; the spec's example body already
shows one. Tell us if global uniqueness is wrong for your deployment and
we will scope it, but scoping it means the lookup needs a second key from
you, so we would rather not guess.

We also did not put an entropy floor on the id. It is now a lookup key,
so a guessable one is enumerable by anyone who can reach the query
surface — the surface is possession-authorized by design, so that the
record can reach you without your holding a gateway credential. If you
want the floor, say so and we will require it at open.

## 3. Confirming your four

For the record, since these are now load-bearing on our side too:

- `debited_units` job/session-scoped — yes, that is the fix we made after
  the one-field-two-meanings mistake.
- `bill(payment_cumulative_units) - bill(payment_cumulative_units -
  debited_units)` for paid-job — correct, and that is exactly the
  cumulative rule; verifying it independently is the point of the field.
- Signed `billed_value_wei` authoritative for sessions — correct. A
  session's charge is the sum of its own debits, and two sessions
  interleaving on one identity do not occupy contiguous stretches of the
  curve, so the signature is what makes it trustworthy.
- Dropping `GetSessionDebits` polling for signed settlements — good.

## 4. Verification you can run

`paid-session/settlement-resolves-gateway-session-id` is in the
conformance suite, so you can check any broker rather than trusting ours.
We verified it fails with the lookup removed, at both the unit and suite
level.

## 5. Unrelated, but it affects your reader

Since the last note we also fixed a payment bug found on Arbitrum One: a
payee that had rotated its recipient rand **credited** payments still
arriving on the retired `work_id`, while every debit against that closed
session failed. Value in, no work billable out — and a winning ticket
banked there is redeemable on chain against a session the payer can never
draw on. It now refuses before validating any ticket and reports
`recipient_rotated`, which is the signal that starts the rebind you
already handle.

Nothing changes for your reader; we mention it because it means a
rotation now produces a refusal your gateways will see, where before it
silently produced a stranded balance.

Settlement-only recipient rotation remains the customer-facing behaviour,
unchanged.
