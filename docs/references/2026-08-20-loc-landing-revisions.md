# Reply to LOC — landing revisions for the two upstream dependencies

Date: 2026-08-20. Response to the LOC implementation-dependency message.
Revisions are on `tasks/lpm-v2`.

## Short answer

Dependency 2 is **landed**, including the piece you did not ask for but
would have needed next. Dependency 1 is **confirmed, filed, and not yet
built** — you diagnosed it correctly, and it is worse than "no
idempotency key": there is no durable sender-side state at all.

## What has landed

| Item | Bead | Revision |
|---|---|---|
| Session `work_id` derives from the payment's `recipient_rand_hash`; all-rejected ticket batches fail closed | `lnm-sqe.1`, `.2` | `87ef866` |
| `per_units` carried end to end, cumulative ceiling billing, payee ledger denominator | `lnm-sqe.3` | `468ae32` |
| Typed `protocol` on `SelectedRoute`; `extra` can no longer shadow the declaration | `lnm-sqe.4` | `3af09c5` |

Specs that moved with them: `paid-session.md` 1.0.3-draft (the
two-identifier rule and the fail-closed rule), `offering-axes.md`
1.0.2-draft §6 (the price pair, the cumulative rounding rule, the three
wire names), manifest schema 2.1.0 (`per_units`, additive).

### On dependency 2 specifically

`SelectedRoute` now carries **`protocol` as a typed field** (field 14),
projected from the signed tuple through the daemon's verified path —
not read out of `extra_json`. That was the half you flagged.

The other half was that `extra` could shadow it. It now cannot: the
signed declaration overwrites, a manifest whose tuple carries
`protocol`, `job` or `session` inside `extra` is **refused outright**
rather than silently corrected, and the broker refuses the same keys at
config load so an operator finds out from their own config instead of
from a manifest rejection that would take every offering they publish
down with it.

Two consequences worth knowing before you migrate:

- **Route fingerprints changed value.** `protocol` is now part of the
  fingerprint, so a manifest that changes protocol invalidates cached
  quotes instead of silently reusing them. Any fingerprint you have
  persisted from a pre-`3af09c5` resolver will not match.
- **The declared axes deliberately stay in `extra_json`.** We are not
  typing `job.transports` or `session.descriptor_schema`. This layer
  gates on nothing inside them, and a typed mirror silently drops any
  axis a later spec minor adds — with the drop baked into the operator's
  signature. Pass-through cannot go stale; the collision rule is what
  makes it safe, and that is what changed.

Pinned nodes in the resolver overlay can now declare `protocol` and
`per_units` too. Strict YAML parsing previously made that impossible, so
a pinned route reached you with no protocol at all — you would have hit
that the moment an operator pinned anything.

## What has not landed: the mint request id

Confirmed, and your reasoning about the consequence is right. Filed as
`lnm-sqe.14` (P1).

The state is worse than a missing field. `CreatePayment` has **no
durable state on the sender side at all** — sessions and their ticket
nonces live in an in-process map. Each call runs `signOneTicket`, which
increments that nonce and signs a fresh ticket, so a retry after an
uncertain response mints a second payment and the customer pays twice.
There is no key to deduplicate on and nowhere to remember the answer if
there were.

So the work is not "add a field", it is:

- `mint_request_id` on `CreatePaymentRequest`, caller-supplied;
- a durable sender-side store — the module already depends on BoltDB for
  the receiver ledger, but the sender has none, and this is the first
  thing that forces one;
- exact-response replay: the identical `payment_bytes`,
  `tickets_created`, `expected_value`, `funded_value_wei`,
  `accepted_quote_ref` and `work_id`, with the same content under a
  reused id refused rather than re-minted, and the guarantee surviving a
  daemon restart.

We are not going to give you a date we would have to revise. What we
will commit to is the shape above, so you can write your call site
against it now: pass an idempotency key you generate per mint intent,
retry freely on an uncertain response, and treat a mismatch error as a
bug in your own key derivation rather than a payment failure.

Until it lands, your read is the correct one — refusing retries after an
uncertain payer response is the safe behaviour, and it is a real cost we
are imposing on you rather than a design preference.

## Not blocking you, but adjacent

`lnm-sqe.5` (top-ups ignore the request id) is the same class of problem
on the broker side and is next in the queue. If your top-up call site is
being written now, write it to send `Livepeer-Request-Id` — the broker
will start honouring it, and sending it early costs you nothing.
