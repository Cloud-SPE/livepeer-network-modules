# Reply to LOC — decisions accepted, contracts specified

Date: 2026-08-20. Response to the LOC team's answers on the three open
questions. Written against the code at `427c57a` on `tasks/lpm-v2`.

## Short answer

All three of your answers are accepted as decided. Below is the
normative text each one implies, at the level of detail you asked for:
the exact denominator mapping and rounding rule, the settlement
signature contract, and the replay re-delivery conditions.

**One new finding first, because it changes your migration work.** While
specifying the denominator we found that the payee ledger does not carry
one at all. This is worse than "dropped in transit", and it lands
directly on the refill and close math you just told us you are
correcting.

## 0. The denominator is missing from the money path, not just the catalog

`payment-daemon/internal/store/store.go:421-424` computes

```
debitWei = price_per_work_unit_wei × work_units
```

with no denominator anywhere. The price it uses is the one the broker
passed at `OpenSession`, which is the offering's raw `amount_wei`
(`capability-broker/internal/server/session_routes.go:69` for sessions,
`internal/server/capability_group.go:78` for jobs) — `per_units` is never
passed. Every other broker billing site does the same: the job total at
`internal/server/middleware/payment.go:450`, the runway estimate at
`internal/server/session_routes.go:411`.

`per_units` is honoured in exactly one place: the settlement record
(`internal/server/middleware/settlement.go:80-84`), which divides by the
denominator carried in the payment.

So for an offering priced at `amount_wei: 100, per_units: 1000` today,
the ledger debits 100 wei per unit — a thousand times the intended rate —
while the settlement attestation reports the correct billed value, and
the resolver publishes a quote flattened to per-unit. Three components,
three different answers, and the one that moves money is the wrong one.

Nobody has been burned by this yet because every offering in service runs
`per_units: 1`. Your migration is what makes it live. We have raised the
denominator work to P0 (`lnm-sqe.3`).

## 1. Carrying `per_units`

Accepted: carried end to end, not pinned.

### The canonical mapping

One value, four names, because two of them are historical:

| Layer | Field | Type |
|---|---|---|
| Broker host config | `price.per_units` | uint64 |
| Broker offerings doc | `per_units` | uint64 |
| Manifest tuple (signed) | `per_units` — **new field**, schema change | uint64 |
| Coordinator envelope | `per_units` | uint64 |
| Resolver `SelectedRoute` | `units_per_price` | uint64 |
| Payment wire `PriceInfo` | `pixels_per_unit` | int64 |
| `SettlementRecord` | `units_per_price` | uint64 |

`pixels_per_unit` is go-livepeer's name for the field and we are not
renaming it — the field number is the compatibility surface, and its own
proto comment already describes this exact use ("setting
`price_per_unit = 1` with a larger `pixels_per_unit` lets pricing go
finer than 1 wei per unit"). Treat the name as legacy and the meaning as
`per_units`.

Two conversion rules the mapping needs, since the types are not uniform:

- **`amount_wei` is a decimal string in the manifest and an `int64` in
  `PriceInfo.price_per_unit`.** A price that does not fit `int64`
  (> ~9.22×10¹⁸ wei per `per_units` units) is unrepresentable on the
  payment wire. The broker will reject such an offering at config load
  rather than silently narrowing it.
- **`per_units` MUST be ≥ 1.** Zero is rejected at config load (already
  true) and at manifest validation (new).

### The rounding rule

Your formula, adopted as normative, with the cumulative property stated
explicitly because that is the part that prevents drift:

```
bill(U) = ceil(U × amount_wei / per_units)
```

where `U` is **cumulative** work units for the session, counted from
open. A refill funds `bill(U_target) − bill(U_funded_so_far)`. No refill
is ever rounded independently, so no sequence of refills can accumulate
error: the only rounding that ever happens is the single `ceil` on the
cumulative total, and it is recomputed, not accumulated.

Ceiling rather than floor, so the payee is never under-funded for work it
has already delivered; both sides compute the identical function, so
"never under-funded" costs the payer at most 1 wei over the life of a
session.

Two invariants that go with it, in the same spirit as the pinned face
value:

- **`amount_wei` and `per_units` are pinned at session open** for the
  life of the session. A price change on the offering applies to new
  sessions only; it can never retroactively move an open session's
  cumulative curve.
- **Jobs bill the same function** with `U` = the units of the single
  exchange, which is the degenerate cumulative case.

## 2. Signing `Livepeer-Settlement`

Accepted, and your reasoning is the right one: the authenticated channel
ends at a customer-controlled SDK, so an unsigned record forwarded from
there is a customer-supplied number deciding the release of customer
credit. That is not a channel-integrity problem we can wave away.

### Signer identity

A **broker-held hot settlement key**, delegated by the orch's cold key
through the signed manifest. Not the cold key itself: the broker is a
network-exposed hot service, and the cold key on secure-orch is what
anchors the orch's on-chain identity. Compromising a broker must not cost
an operator its identity.

The delegation is a new block in the manifest payload — which your
resolver path already fetches, verifies against the orch's on-chain
`eth_address`, and rejects on rollback via `publication_seq`. So
verification-key discovery for settlement is exactly the trust path LOC
already implements for routes. No second PKI, no key-server, no new
fetch.

### Canonical payload

JCS (RFC 8785) canonicalization and secp256k1, matching the manifest, so
your verifier reuses its existing primitives. The signed payload binds:

- `sender` (payer address) and `recipient` (orch eth address)
- `work_id` (the authoritative `hex(recipient_rand_hash)`, per the
  previous round)
- `quote_ref`: `quote_id`, `quote_version`, `constraint_fingerprint`,
  `route_fingerprint`
- `work_unit`, `per_units`, `amount_wei`
- cumulative `claimed_units` and `debited_units`
- `billed_value_wei`, `funded_value_wei`, `outcome`
- `settlement_seq` and `issued_at`

### Freshness and replay binding

`settlement_seq` is monotonic **per `work_id`**, and LOC rejects any
record whose seq is ≤ the highest already accepted for that `work_id` —
the same rollback rule you already apply to `publication_seq`, so the
pattern is one you have implemented once already. `issued_at` gives you a
staleness window to enforce on top; we suggest you reject records older
than your own settlement SLA rather than us picking a number for you.

The pair `(work_id, settlement_seq)` is the replay binding: a record is
meaningful for exactly one session and one point in its life, and cannot
be replayed into another session or an earlier point in the same one.

### Rotation

Publish a manifest with a new delegation and a higher `publication_seq`.
Records signed by the previous key stay verifiable for a documented
overlap window — we propose the manifest carries the previous key with an
`expires_at`, so a record in flight during rotation does not fail
verification. LOC verifies against any non-expired delegated key.

### The balance object

Same treatment where it is authoritative. Two ways to consume it and you
should have both:

- **Signed snapshot.** The `balance` object gains the same signature
  envelope, so an SDK-forwarded snapshot is verifiable.
- **Direct query.** A server-to-server authenticated read of the
  session's settlement/balance state, so LOC can bypass the SDK entirely
  for anything financially material. This is the option we would build
  our own reconciliation on.

## 3. Re-delivering credentials on replay

Accepted, with every condition you set. Recorded as the contract:

A session-open replay returns the **usable** recorded outcome —
credential and grants included — if and only if all of:

- same `Livepeer-Request-Id`;
- identical full request fingerprint (the fingerprint we owe you anyway
  from the previous round, `lnm-sqe.7`);
- same cryptographically authenticated payer identity — the sender
  sealed on the original session's payment, re-proved by the replay's own
  payment envelope, not asserted;
- the recorded outcome is returned exactly, with no re-execution.

Request-id possession alone authorizes nothing. If the retry cannot be
authenticated as the original payer, the open is refused and
abandon-and-settle remains the fallback — the failure path, never the
normal retry path.

Storage: credentials and grant secrets sealed at rest and retained only
for the replay window, which we will bound by the session's lease. The
store already seals descriptor private material under AES-256-GCM with a
key from `session_store.sealing_key_file`
(`capability-broker/internal/sessionstore/sessionstore.go:97-102`), so
this reuses existing machinery rather than inventing key handling.

This reverses the spec's current exactly-once secret delivery. We think
you have the better of the argument: a funded, undrivable session is a
worse failure than a secret delivered twice to the same authenticated
payer over TLS.

## 4. Rotation working session

Confirmed on our side. Our participant and scheduling are being settled
separately and will come in the next message rather than holding this
one.

## Your LOC-side correction

Noted, and it is the mirror of ours: your initial funding honours
`units_per_price` while refill and close assume 1; our settlement record
honours it while the ledger assumes 1. Both are the same bug seen from
opposite ends of the same seam, which is a reasonable argument for the
cumulative rule above being written once, normatively, and referenced by
both implementations rather than re-derived.

## Tracked

- `lnm-sqe.3` (P0) — denominator end to end, canonical mapping, cumulative
  rounding rule, and the ledger fix.
- `lnm-sqe.13` — settlement signing: delegation in the manifest, canonical
  payload, seq/freshness, rotation overlap, signed balance snapshot plus
  the direct authenticated read.
- `lnm-sqe.12` — replay re-delivery under the four conditions above.
- `lnm-sqe.7` — the open fingerprint the replay contract depends on.
- `lnm-sqe.1` (P0) — session `work_id`, which everything above binds to.
