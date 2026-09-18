# To LOC — your two fixes, and three field changes that affect your reader

Date: 2026-08-21. Branch `tasks/lpm-v2`, head `5948242`.

## Your two items

Both confirmed and landed in `5948242`.

**1. `gateway_session_id` in the signed session settlement.** Your
reasoning is right and it is the part we had wrong: a record carrying
only `session_id` and `work_id` gives you nothing you issued yourself.
`session_id` is broker-local and reaches you through the
customer-controlled SDK — the channel the signature exists to distrust —
and `work_id` can be shared by several sessions. The broker already
stored `gateway_session_id`; it simply never reached the payload.

It is now inside the signature and in the `GET /v1/settlement/{id}`
response. `paid-session` 1.0.7-draft §3.3.1 requires it.

**2. The rebinding top-up returned the predecessor `work_id`.** Exactly
as you described: the handler reloaded the record, used the fresh one for
the balance object, and returned `work_id` from the stale snapshot. A
gateway that had just rotated got back the identity it left, whose every
ticket the payee rejects. Fixed, with a handler-level test whose negative
control reproduces your report.

## Three changes you should read before your reader lands

These are ours, from work since the last note, and two of them will fail
closed for you if you have already built against the older shape.

**`debited_units` changed meaning on the job path.** It briefly carried
the *payment identity's* cumulative on `paid-job` while carrying the
*logical session's* total on `paid-session` — the same field meaning two
things depending on which protocol produced the record. That was our bug.
It is now always scoped to the exchange (job) or the logical session
(session).

**The identity's running total moved to `payment_cumulative_units`.**
That is the field that places a charge on the cumulative curve.

**Billing is cumulative across EXCHANGES, not only across ticks.** A
paid-job exchange is one increment on its payment session's curve, so the
second job on a shared ticket session costs
`bill(2u) − bill(u)`, which is *less* than an independent ceiling
whenever a remainder carried. Verified on Arbitrum One: three jobs at 42
units, 100 wei per 1000, charged 5 then 4 then 4 — not 5, 5, 5.

So the check you built now reads, for **`paid-job`**:

```
billed_value_wei == bill(payment_cumulative_units)
                  − bill(payment_cumulative_units − debited_units)
```

and it is fully satisfiable from the record alone.

## One limit worth knowing before you rely on it

For **`paid-session`**, a charge is **not** recomputable when the payment
identity is shared. A session's charge is the sum of its own debits, and
two sessions interleaving on one `work_id` do not occupy contiguous
stretches of the curve. The signature is what makes a session's charge
trustworthy; `payment_cumulative_units` lets you verify the **aggregate**
across every record on an identity and notice a missing one.

We would rather you hear that from us than find it while reconciling.
`offering-axes` 1.0.6-draft §6.1 states it.

## A question back

You asked for `gateway_session_id` on sessions because you cannot bind a
record to your own session otherwise. Does the **job** path have the same
problem for you?

A job settlement carries `job_id` and `work_id`, both broker-minted, and
`Livepeer-Request-Id` — the id your side chooses — is not in the signed
payload at all. By your own argument that leaves a job record bindable
only through the SDK. You did not raise it, so you may have a different
path to it on the job side; if you do not, say so and we will put the
request id in the signature the same way.

## What has NOT been verified

Rotation has never run against a real chain. The rebind path, the
generation chain, and `payment_unrecoverable` are covered by unit tests
and by conformance, but no real payee has rotated a rand under a live
session. Everything else in this note was verified on Arbitrum One.
