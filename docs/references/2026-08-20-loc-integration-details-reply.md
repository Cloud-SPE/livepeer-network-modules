# Reply to LOC — the three integration details

Date: 2026-08-20. Response to the LOC team's integration-detail message.
Written against the code at `2bfbe3c` on `tasks/lpm-v2`.

## Short answer

All three are accepted and specified below. Two of them improve the
design we sent: the rotation-generation link closes a hole our own
`settlement_seq` rule had, and your "LOC does not verify manifests
itself" correction moves the delegation to where it actually gets
consumed.

## 1. Delegations reach LOC through the resolver, not the manifest

You are right and our previous answer was written for the wrong
consumer. We said discovery "rides the manifest you already verify" —
you do not verify manifests, the service-registry daemon does, and you
consume its verified projection. Routing key discovery through a document
you never fetch would have made you build manifest verification for this
one field.

**Decision: the delegations are projected onto `SelectedRoute`**, beside
the route data you already snapshot. The registry daemon stays the
verifier (it already recovers and compares secp256k1 signatures over the
JCS-canonical manifest), and it projects only what it has verified.

The projection carries **all currently-valid delegations**, not just the
active one, so an in-flight record signed by an outgoing key still
verifies during an overlap:

- `settlement_keys[]`, each with the public key, `not_before`,
  `expires_at`, and the `publication_seq` of the manifest that
  introduced it
- ordered newest first; the first entry is the signer for records
  emitted now

Persist the set with your immutable route snapshot as you described. A
record verifies if it was signed by any key in the snapshot whose
validity window contains the record's `issued_at`. Rotation is then a
manifest publish with a higher `publication_seq`, and the rollback rule
you already enforce on `publication_seq` protects the delegation set too
— which is the property we wanted from the manifest path and now get
without you fetching manifests.

Because the delegation set is projected rather than fetched, it inherits
the resolver's freshness semantics. If your snapshot is older than a
rotation *and* the overlap window has closed, verification fails closed
and you fall to the direct query in §3 — which is the correct behaviour,
not a gap.

Tracked in `lnm-sqe.13`.

## 2. Binding rotation into the signed contract

Accepted, and this is a real hole in what we sent. We proposed
`settlement_seq` monotonic **per `work_id`**. Rotation mints a new
`work_id`, so that rule would have silently restarted the ordering
counter at each rotation and left you unable to order two records from
one logical session. Worse, the cumulative billing rule would have
restarted with it, and `ceil()` over a restarted cumulative total
under-bills.

**The signed payload binds both identities and the chain between them:**

- `session_id` — the stable `sess_<uuid>` broker resource id. This is now
  load-bearing rather than merely convenient, which is a second reason it
  survives the identity decision from two rounds ago.
- `work_id` — the payment identity in force for this record.
- `rotation_generation` — 0 at open, incremented on each rotation.
- `predecessor_work_id` — empty at generation 0, otherwise the `work_id`
  this generation replaced.

**`settlement_seq` becomes monotonic per `session_id`**, not per
`work_id`. Your rollback rule keys on `session_id`; the replay binding is
`(session_id, settlement_seq)`.

**Cumulative quantities are per logical session and never reset on
rotation.** Funding, however, is per generation: each `work_id` has its
own payee-side balance. So the record reports both, and you need both:

- `debited_units` / `billed_value_wei` — cumulative across the whole
  logical session, spanning every generation. This is what your
  independent recompute checks.
- `generation_debited_units` / `generation_billed_value_wei` /
  `generation_funded_value_wei` — scoped to this `work_id`, so each
  funding envelope reconciles against what it actually paid for.

The invariant across a rotation: the sum of generation subtotals equals
the cumulative total, and cumulative `billed_value_wei` is computed once
over the cumulative units — never as a sum of per-generation ceilings,
which would reintroduce exactly the per-chunk rounding drift the
cumulative rule exists to prevent.

Tracked in `lnm-sqe.10` and `lnm-sqe.13`.

## 3. The authoritative billing quantity

**Cumulative `debited_units`.** Your expectation is correct, and your
recompute is the right check:

```
billed_value_wei == ceil(debited_units × amount_wei / per_units)
```

with `debited_units` cumulative over the logical session and `amount_wei`
/ `per_units` pinned at open.

`claimed_units` is reported but is **not** the billing quantity. It is
what a runner asserted; `debited_units` is what the ledger moved. In the
current engine they cannot diverge — the debit is issued before the
commit, and the claim watermark and debit total advance in a single
atomic store update
(`capability-broker/internal/sessionengine/engine.go:437-478`), so a
failed debit commits neither. That makes the equality an invariant we can
assert rather than a coincidence: the signed record will carry both, and
a record where they differ is a defect on our side, not an accounting
subtlety on yours. Treat a mismatch the same way you treat a signature
failure.

### The direct-query contract

`GET {worker_url}/v1/settlement/{work_id}` — also resolvable by
`session_id`, since after a rotation you may hold a stale `work_id`.

**Response:** the same signed envelope as the header record, byte for
byte the same canonical payload and signature — so your verifier is one
code path, not two. Plus `state` (`open` | `winding_down` | `closed`) so
you can distinguish an interim snapshot from a final settlement, and
`issued_at` regenerated per query (a fresh signature over fresh
cumulative totals; the payload is a statement about now, not a cached
blob).

**Status codes:** `200` with the envelope; `404` for an unknown
`work_id`/`session_id` past the retention window; `503` while the broker
cannot reach its payment ledger, so you retry rather than treat a
transient as absence. Never a `200` with stale-but-unlabelled data.

**Authentication, and a recommendation you may disagree with.** We
propose the query needs **no shared secret**: TLS server authentication
against the `worker_url` in your verified route snapshot, plus possession
of the `work_id` — which is a 32-byte payee-issued value, not a guessable
identifier — with rate limiting on the surface.

The reasoning: the response's integrity does not come from the channel,
it comes from the signature you already verify. Adding a shared bearer
would mean LOC holding one credential per orch and every orch
provisioning one for LOC — an N×M secret distribution problem solved for
a property the signature already gives you. Operators who want caller
authentication anyway can put mTLS in front of the surface; the contract
does not change.

If you need caller identity for audit rather than for integrity, say so
and we will design it — but it would mean registering an LOC verification
key with each orch, which is real cost we would rather not spend on v2
unless it buys you something specific.

### Delayed delivery

Agreed, and it falls out of the above: the final signed record is durable
and regenerable for a documented retention window, so a settlement that
arrives late through an SDK is never lost — you re-query, verify, and
release. We will publish the retention floor with the endpoint; tell us
the window your dispute process needs and we will make sure the floor
clears it.

## Status

No open design questions from our side either. The contracts above are
what we will implement; anything we discover while implementing them that
contradicts this document, you will hear about the way you heard about
the ledger denominator.

Rotation working session: our participant and scheduling still follow in
a separate message.
