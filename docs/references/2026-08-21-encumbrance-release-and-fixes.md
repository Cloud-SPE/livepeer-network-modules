# To LOC and the meeting team — the seed bug, the silent open, and encumbrance release

Date: 2026-08-21. Branch `tasks/lpm-v2`.

Three threads. Two were bugs, one is a design question worth answering
carefully because the honest answer is narrower than the question hopes.

---

## 1. `--chain-seed` could not start (LOC)

You ran the command I documented and it does not boot. You are right, and
the cause is worth stating plainly: **I documented it without ever running
it.** `parseFlags` forces `discovery=overlay-only` whenever `--dev` is
set, and validation refuses `--chain-seed` alongside overlay-only. Two
rules that are each correct and jointly fatal. Every unit test I wrote
passed while the binary refused to start, because each tested a layer.

Fixed: `--dev` no longer forces overlay-only when a seed is present. Plain
`--dev` still means overlay-only, and an explicit
`--discovery=overlay-only` alongside a seed is still refused — overlay-only
never reads the chain, so the seed would be silently ignored.

The regression is both halves you asked for: a `parseFlags` test asserting
`discovery=chain` survives, **and** a `run()` test that starts the daemon
on the documented argv. I also ran the real binary:

```
=== DEV MODE — in-memory providers, throwaway keys, no chain ===
INFO daemon ready mode=resolver socket=/tmp/... discovery=chain version=dev
INFO grpc listening socket=/tmp/...
```

gosec G703 is suppressed with the same `//nolint:gosec` + reason the
other operator-supplied paths use (`--static-overlay`, keystore, password
file). A pre-existing staticcheck S1016 in `coordinator_envelope.go` is
fixed in passing, so `make lint` is clean rather than clean-except-one.

## 2. An omitted `gateway_session_id` was accepted silently (meeting team)

Confirmed exactly as you describe, and thank you — this is the better half
of the report, because it is the failure `1.0.8-draft` was written to
prevent reached by the road nobody was watching.

Uniqueness was guarded only when the field was present, so a client that
never sent it opened sessions indefinitely and received settlements
carrying an empty value for the only identifier its consumer issues
itself. Your second-order point is also right: two such clients do not
collide either, so a broker retains two unresolvable records rather than
refusing one open.

An open with no `gateway_session_id` — omitted, empty, or whitespace — is
now refused with `invalid_request` (400), which is the code you suggested
and the right one: `gateway_session_id_reuse` describes a different
mistake. `paid-session` 1.0.9-draft §3.3.1 states it REQUIRED.

## 3. Rebinding without the socket (meeting team)

**Your reading is right**, and it is now a sentence in §3.3.1 rather than
something to infer.

The predecessor to declare is the last `work_id` you held. That is by
definition the identity the broker still has recorded — the rotation
happened payee-side, and the broker learns of it from the same refusal you
did. A gateway that lost its own state reads it back from
`GET /v1/session/{id}`, which returns the session's current `work_id`.

So `session.rebound` and the §8 socket are an optimisation, not a
precondition. You were right to ask: the natural reading of "declares its
predecessor" is that you must have been told about the rotation first, and
the only place you are told was a channel you do not hold.

---

## 4. Encumbrance release for an envelope never admitted (LOC)

The short answer: **nothing proves non-use before expiry, and expiry is
the only unconditional release. It is enforced by the chain, and the
deadline is short and computable.**

### Why revocation does not exist

A signed payment envelope cannot be recalled. The sender's signature *is*
the authorization; handing it over is irreversible. There is no
per-envelope revocation primitive, and adding one would mean a
sender-controlled way to invalidate a payment a payee may already be
relying on — which is the property payees need most.

The nuclear option — the sender withdrawing its deposit and reserve — is
not per-envelope: it affects every session with every payee and carries an
unlock period. Not a mechanism you can use per job.

### Why non-use cannot be proven before expiry

Three things compound:

1. A payee holding the envelope can redeem a winner at any point in the
   window.
2. A payee-signed "I never saw this" is a **claim against its own
   interest** — good evidence, and we can give you a machine-readable form
   of it if you want one — but it is not proof. An honest payee that has
   simply not processed the envelope yet would sign it truthfully and
   process it afterwards.
3. **You cannot narrow the exposure yourself.** A ticket wins iff
   `keccak(sig, recipientRand) < winProb`, and `recipientRand` is a secret
   the payee holds until redemption. So no third party — including you —
   can determine whether an un-admitted envelope's ticket won. Every
   unexpired envelope must be treated as potentially winning at **full
   face value**, not at its expected value.

Point 3 is the one that decides the design. It is why "wait for expiry" is
not us being conservative; it is the only state in which the answer is
knowable without the payee's cooperation.

### What expiry gives you, and when

A ticket carries `creation_round` and `creation_round_block_hash`. The
TicketBroker needs that block hash to verify a winning ticket, and it stops
being available beyond the validity window — **2 rounds** — so redemption
reverts. That is a chain property, not a policy either daemon implements.
An operator can configure a shorter window for their own redeemer; nobody
can extend one.

So: **release the encumbrance once `expires_after_round` is behind the
current round.** No attestation, no trust in either party, no reliance on
a caller assertion or a timeout you chose.

To make that mechanical rather than something you parse out of the
envelope, `CreatePaymentResponse` now returns the deadline at mint:

```
creation_round      = the round the tickets were minted in
expires_after_round = creation_round + 2
```

The deadline therefore travels with the envelope from the moment it is
issued, which is exactly the "issued but never admitted" case — you have
it before anything goes wrong, without a round-trip to a broker that may
be the thing that failed.

### The recovery contract we would propose

1. **Deterministic release at `expires_after_round`.** Unconditional and
   chain-enforced. This is the backstop that always works, including when
   the broker never responds at all.
2. **Early release on a broker refusal**, when you get one: a refusal
   before admission means nothing was debited and no settlement exists.
   That is safe to act on because the broker never held the envelope in a
   spendable state — but it only covers the cases where a broker answered.
3. **Optional early release on a payee non-use attestation.** We can build
   a signed one over `(sender, recipient_rand_hash, nonce)`. It is evidence
   against interest, not proof; whether it is worth the residual risk is
   your call, and we would rather you decide that explicitly than have us
   present it as a guarantee.
4. **Bound the exposure at mint.** Max exposure per envelope is the ticket
   `face_value` — and the gateway chooses face value and win probability
   when it mints. If a 2-round encumbrance at your current face value is
   uncomfortable, the lever is the minting parameters, not the protocol.

### What we are NOT claiming

We are not claiming the payee's local view is authoritative.
`GetRedemptionStatus(ticket_hash)` reports what that payee did, which is
useful for reconciliation and useless as proof against a payee that
misreports. The authoritative record is `usedTickets[ticketHash]` on
chain, which you can read without trusting either daemon.

If a 2-round window is too long for your product, say so and we will
discuss what a shorter one would require. It is a chain constant, so the
answer is likely to be "bound the face value" rather than "shorten the
window" — but it is worth having the conversation with real numbers rather
than assuming.

---

## References

| Item | Where |
|---|---|
| `--chain-seed` start fix + regressions | lnm-6y1, closed |
| Omitted `gateway_session_id` refused | lnm-b7b, closed; paid-session 1.0.9-draft |
| Rebind without the socket | paid-session 1.0.9-draft §3.3.1 |
| `creation_round` / `expires_after_round` at mint | `payer_daemon.proto` fields 7–8 |
| Chain validity window | `settlement.ChainValidityWindowRounds` |
