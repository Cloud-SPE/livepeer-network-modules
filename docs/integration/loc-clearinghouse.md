# LOC — what to change

You verify signed evidence and decide what a customer is charged. This
is what the broker gives you and what it deliberately does not.

## 1. Verifying a settlement

`Livepeer-Settlement` is base64 of `{payload, signature}`. The payload is
JCS-canonical JSON of a `SettlementRecord`; the signature is EIP-191
secp256k1 over **those exact bytes**.

Verify over the payload **as received**, never over a re-serialization —
re-encoding silently repairs a broker that does not canonicalize, which
is the thing the canonical form exists to catch. Use
`livepeer-network-protocol/verify`, which is the same code the
conformance suite uses.

The recovered signer must be a key the orch's manifest delegates in
`settlement_keys`, with a validity window containing `issued_at`.

## 2. Binding a record to your job

| Path | Bind on |
|---|---|
| paid-job | `request_id` — the id **you** issued, signed into the record |
| paid-session | `gateway_session_id` — same principle |

`job_id` and `session_id` are broker-minted and reach you through the
customer's SDK, which is the channel the signature exists to distrust.
`work_id` is shared across every exchange on one ticket session.

`gateway_session_id` must be globally unique and ≥96 bits of CSPRNG
entropy; a UUIDv4 qualifies. It is collision and enumeration resistance,
not authentication.

## 3. Billing arithmetic

- **paid-job:** verify as
  `bill(payment_cumulative_units) - bill(payment_cumulative_units - debited_units)`.
- **paid-session:** the signed `billed_value_wei` is authoritative. Two
  sessions interleaving on one identity do not occupy contiguous
  stretches of the curve, so it cannot be recomputed from the record.
- `debited_units` is the billing quantity and is scoped to the exchange
  (job) or the session. `actual_units` is what was measured.

## 4. Terminal outcomes

| Evidence | Outcome |
|---|---|
| valid signed settlement | settle on it |
| no terminal evidence | remain unresolved |
| your operational deadline passes with none | conservative full charge, marked as such |
| signed non-admission | retain as audit evidence |

**There is no automatic refund**, and the reason is worth carrying in
your code comments: `Livepeer-Request-Id` is not cryptographically bound
into `payment_bytes`, so a non-admission record does not retire the
envelope. The same envelope can be presented under a different request
id, and a governance increase to `ticketValidityPeriod` can revive it
after the record was signed.

## 5. Expiry is conditional

`CreatePayment` returns `creation_round`, `expires_after_round`
(`creation_round + period - 1`, the last redeemable round) and the
`ticket_validity_period` those came from, plus an `observed_at`.
`GetDepositInfo` returns `current_round` from the same clock and the
chain's current period, read fresh.

```
not currently spendable  iff  current_round > expires_after_round
                         and  the period is unchanged
```

Equality stays encumbered. Missing, zero or regressing values stay
encumbered. Compare the recorded period against the chain's to **detect**
a governance change — it is telemetry, not a trigger to re-encumber,
which is not implementable once credit has moved.

## 6. Reconciling without the customer

```
GET /v1/exchange/{request_id}
POST /v1/non-admission/{request_id}
```

The first returns the outcome keyed on your own id — see the gateway
guide for the table. Use it **before** applying a conservative charge:
the broker may hold a settlement the customer withheld.

The second returns a signed `NOT_ADMITTED` record. It requires full
context — protocol, work id, sender, recipient, quote id and version,
both fingerprints, and `job_issued_at` — and refuses if any field is
missing or malformed. It also refuses when the broker's own records
begin after your job was issued (`coverage_gap`), or when the request
turns out to have been admitted (in which case it returns the outcome
rather than a bare refusal).

`coverage_started_at` is an **attributable broker assertion, not proof of
uninterrupted storage**. It detects a wiped store, because a fresh one
re-stamps it. It does not detect a restored backup. If you want rollback
resistance, the anchor has to live outside the broker's store.

## 7. Sidecar topology

The payer sender and the registry resolver are meant to run co-located
with you over unix sockets, not exposed over a network — they are
trusted interfaces. `--socket` on both.
