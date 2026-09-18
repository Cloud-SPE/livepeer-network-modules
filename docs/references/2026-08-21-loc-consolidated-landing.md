# To LOC — everything raised has landed, and one correction to your plan

Date: 2026-08-21. Branch `tasks/lpm-v2`, head `48fb90c`, pushed.

This replaces the five separate replies drafted since 2026-08-19. They
were written as each round arrived; sending them in sequence now would be
worse than one message, because every open item in them has since closed.

## Read this part first

**`work_id` alone will not fix the cross-job replay you reported.** You
asked for "a signed per-job identity — preferably payment `work_id`", and
that is necessary but not sufficient.

On `paid-session/v1` a `work_id` identifies one session, so your instinct
holds. On `paid-job/v1` it does not: `work_id` is the hex
`recipient_rand_hash` of the **ticket session**, and every job minted
against that session shares it. Two exchanges on one ticket session would
have interchangeable settlements, which is the property you were trying
to eliminate.

The signed payload therefore carries **`job_id` as well**, and
`SettlementRecord` gained a `job_id` field. If you are building
verification now, bind on `job_id` for jobs and `session_id` for
sessions; `work_id` is the payment identity and belongs in the record,
but it is not the anti-replay key on the job side.

## Your four corrections — all landed in `c971fa3`

| # | What you reported | What landed |
|---|---|---|
| 1 | `CreatePayment` replay not atomic | The mint id is **reserved durably before anything is signed**, and calls sharing an id are serialized per-id. A reservation with no response behind it **refuses** the retry rather than re-signing: it cannot prove whether the first attempt produced a ticket, and re-signing on a maybe is how a payer pays twice. Refusing costs you a new idempotency key; guessing costs money. |
| 2 | No `issued_at` on job settlements | Present, RFC3339 with nanosecond precision. |
| 3 | No signed per-job identity | `job_id` **and** `work_id` inside the signature — see the correction above. |
| 4 | `billed_value_wei` floors when `per_units > 1` | Now `payment.BillFor`, the same ceiling the ledger and the session path compute. |

Tests cover every case you asked for: concurrent identical mints (with a
negative control — with the lock removed, racing callers fail, so the
test is a real regression guard), the signing-to-record crash window, key
validity boundaries at both edges, cross-job settlement replay, and a
remainder-producing `per_units > 1` case.

We also added what your key-window question implied: the broker **refuses
to sign outside the validity window** its delegation was published with,
so it cannot emit evidence that fails verification somewhere else, later,
for reasons invisible from the signing side.

## Verified on mainnet, not only in tests

Your finding #4 was ours to have caught and we did not, because every
fixture we had — including our first Arbitrum One probe — ran
`per_units: 1`. That is the single denominator at which flooring and
ceiling agree, so the defect was structurally invisible to us.

Re-run on Arbitrum One at `per_units: 1000`:

- offering 100 wei per 1000 tokens, work 42 tokens → 4.2 wei
- **payee ledger debited 5 wei**
- **signed record `billed_value_wei` = 5 wei**

Ceiling, not floor, and the two agree — the exact disagreement you
predicted. Conformance gained a `fractional` offering at that price so
the suite now exercises a denominator where the arithmetic differs.

## Three defects the same mainnet run found, which you should know about

These were ours, none of them reachable by any mock:

1. **Every paid exchange billed ZERO.** `GetTicketParams` must create the
   payment session before a sender can mint, and it knows nothing about
   what work costs, so it seeded price `0`. The broker's `OpenSession`
   then arrived with the real price, found the id present, and discarded
   it. Fixed: pricing is write-once from a distinct *unset* sentinel
   (zero is a legal price and must not mean "unset"), a conflicting
   re-price is refused, and billing an unpriced session fails closed.
2. **The payee billed at a price nobody signed.** It used the broker's
   assertion and never read `expected_price` from the payment. It now
   cross-checks and refuses a mismatch — so a compromised or buggy broker
   cannot bill at a rate you never agreed to.
3. **Work was served against a zero balance.** No pre-flight check
   existed for a single exchange. Now `insufficient_balance` (402) before
   the backend runs.

## Everything else you raised, earlier rounds

- **Registry**: typed `protocol` on `SelectedRoute`; `extra` can no
  longer shadow the signed declaration (a colliding manifest is refused);
  overlay pins can declare `protocol` and `per_units`. **Route
  fingerprints changed value** — anything persisted from before `3af09c5`
  will not match.
- **`per_units`**: carried end to end (manifest 2.3.0 → coordinator →
  resolver `units_per_price` → payment → settlement), with the cumulative
  ceiling rule normative in `offering-axes.md` §6.1 and pinning in §6.2.
- **Settlement signing**: signed JSON envelope on **both** protocols,
  JCS + EIP-191 secp256k1, delegated hot key discovered through
  `settlement_keys` on the route you already consume. Signature omitted —
  not emptied — when a broker holds no delegation.
- **Idempotency**: session opens fingerprinted and re-delivering
  credentials on an identical replay; top-ups honour `Livepeer-Request-Id`;
  job replays bind the body by streaming digest rather than its length.
- **Rotation**: designed, implemented and spec'd (`paid-session` §3.3.1).
  Declared rebind on the top-up you already retry, three verification
  rules, predecessor settled before close, bounded, and settlement-only
  from the customer's side exactly as you asked.
- **Streamed job claims**: `GET /v1/settlement/{id}` serves paid-job keyed
  by `Livepeer-Job-Id`, so a client that cannot read trailers is no longer
  forced to choose between billing zero and blocking.
- **Reconciliation**: `GetSessionDebits` is marked deprecated and
  unimplemented. The payer keeps no debit ledger; the payee-side signed
  settlement is the source.

## What we have NOT verified

`paid-session` has never run against a real chain. Open, top-up, rotation
rebind and settlement have only ever run against the mock — and the mock
is precisely what hid three of the defects above. We are doing that run
next and will report what it finds, including anything that contradicts
this document.

## Landing commits

`48fb90c` is the branch head. The four corrections are `c971fa3`. The
mainnet-run fixes are `c16c246` and `f4a9e0c`. Rotation is `1dfbd33`,
`d0fee0d`, `5b8f9f1`. Settlement signing is `f9d646d`, `309394c`,
`4ef504d`.

`tasks/lpm-v2` is staying a branch for now — more work and testing ahead
before any release — so keep pinning SHAs.
