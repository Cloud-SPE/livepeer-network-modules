# Reply to LOC — mint id namespace, retention, and both blockers

Date: 2026-08-20. Response to the LOC implementation-dependency message.
Revisions on `tasks/lpm-v2`.

## Short answer

Both release blockers are **landed**.

| Item | Bead | Revision |
|---|---|---|
| Top-up idempotency on `Livepeer-Request-Id` | `lnm-sqe.5` | `7c96f10` |
| `CreatePayment` mint id with durable replay | `lnm-sqe.14` | `806f6bb` |

Your GC question was the right one to ask before we built it, and it
changed the design. The answer is a permanent tombstone, for the reason
you gave.

## The namespace

`mint_request_id` is scoped to **the daemon's own sender identity** — its
keystore address. Two callers sharing one daemon share one namespace, so
ids must be unique within it: a UUIDv4, or a caller-prefixed form like
`loc:<uuid>` if you want the origin legible in logs. Max 128 bytes.

The storage key is `hash(sender_address, mint_request_id)`, so rotating
the daemon's key cannot collide with ids issued to the old one.

The id is a **promise about content**. It is bound to a fingerprint over
recipient, ticket-params base URL, accepted price and funding intent; the
same id with different content is refused with `INVALID_ARGUMENT` rather
than answered with the earlier batch, which you never asked for.

## Retention, and why a tombstone

You are right that retention alone cannot be safe, and we are not going
to pretend otherwise: if an evicted key became mintable again, a
sufficiently delayed retry pays twice and nothing in the system reports
it. We looked for a provably-safe lifecycle rule that would let records
age out completely and did not find one — ticket expiry does not help,
because a re-mint produces a *fresh* valid ticket rather than an
unredeemable one.

So the daemon keeps two records:

- **The replay payload** — full response, evicted on `--mint-retention`
  (default 24h, operator-configurable).
- **A tombstone — permanent.** The key's hash and its request
  fingerprint, no payload.

A key the daemon has ever minted against is never treated as new. Past
the retention window a retry gets `FAILED_PRECONDITION` — an explicit
"this id was used and its replay record has expired; use a new id" —
never a second mint. Refusal is loud and recoverable; a silent double-pay
is neither.

**Cost of permanence:** the tombstone is a hash with no payload, ~80
bytes per mint. Ten thousand mints a day is roughly 300 MB a year, and it
compacts to nothing but hashes. If that ever matters we can talk about a
monotonic-sequence namespace that bounds it to O(1) per caller, but it
would put an ordering constraint on your id issuance and we would rather
not impose that for storage you probably do not miss.

## One interaction you need in your call site

**A recovery retry after `INVALID_RECIPIENT_RAND` is a NEW mint intent
and needs a NEW id.** The original id would replay the very payment the
payee rejected. The rotation mints against a fresh recipient rand, so it
is a different payment by construction — treating it as a new intent is
honest rather than a workaround.

So: one id per mint intent, retried freely under that id; a rotation
recovery gets a fresh one.

## What else changed under you

`lnm-sqe.5` landed with the top-up contract you would have written
against anyway, plus two details worth knowing:

- **Replay precedes every other check**, terminal and `refill_refused`
  included. A retry of a top-up that succeeded returns its recorded
  answer even if the session has since ended; only a *new* request id
  meets a refusal.
- **An already-credited envelope** — every ticket rejected for nonce
  replay — is not a payment failure. You get the current lease back,
  unextended. That is the window between the daemon's credit and the
  broker's record of it, and it is why your retry is safe even if it
  arrives mid-crash.
- The control-WS `session.topup` frame now carries `request_id` in the
  body, since a frame has no headers.

`paid-session.md` is at 1.0.4-draft with all of it stated normatively.

## Where that leaves your release

Both blockers are in. Neither has been exercised against a real chain —
our gates are unit, conformance (33 scenarios) and the dev broker — so
the first end-to-end run on a funded deposit is worth doing together
rather than discovering separately.
