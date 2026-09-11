---
spec_name: wholesale-account
version: 1.1.0-draft
status: draft
last_updated: 2026-09-11
---

# Wholesale account and spend authorization

This specification separates probabilistic-ticket funding from permission to
run work. It is shared by `paid-job/v1` and `paid-session/v1`; it is not a
third interaction protocol and introduces no capability taxonomy.

The key words MUST, MUST NOT, SHOULD, and MAY are interpreted as in RFC 2119.

## 1. Stable account

The receiver maintains one economic account per `(chain, payer, payee,
denomination)`. Tickets credit this account. Requests, sessions, capabilities,
offerings, quotes, and recipient-random `work_id` generations do not own the
balance.

The account tracks credited, reserved, debited, and available value. Every
mutation MUST preserve:

```text
credited + inbound adjustments
  = available + reserved + debited + outbound adjustments
```

`chain_id` and `denomination` are signed authorization fields and returned in
account observations. The initial implementation supports denomination `wei`;
an authorization for another chain or denomination cannot be replayed into the
local account.

Admission and settlement MUST be durable and atomic. Concurrent authorization
admissions MUST NOT reserve the same available value.

## 2. Single-purpose authorization

`SpendAuthorization` is deterministic protobuf signed by the payer using
keccak256 plus Ethereum personal-sign (EIP-191). Its domain is exactly
`livepeer-spend-authorization/v1`.

The signed bytes are unambiguous:

```text
payload_bytes = deterministic_protobuf(SpendAuthorizationPayload)
payload_hash  = keccak256(payload_bytes)
signing_hash  = keccak256("\x19Ethereum Signed Message:\n32" || payload_hash)
signature     = secp256k1_sign(signing_hash) as 65-byte R || S || V
```

`V` MUST be 27 or 28. `BigUInt` zero MUST use its canonical empty byte
encoding; positive integers MUST use minimal unsigned big-endian bytes.

The receiver MUST verify the signature and reject an authorization unless its
payee is the local payee, its route and quote match the broker-selected offer,
its request or session identity matches the invocation, it is currently valid,
and its requested debit is within payer and receiver policy.

For a job, `session_id` is empty and `request_digest` is the SHA-256 digest of
the exact body. For a session, `session_id` is required and the digest commits
to the exact session-open body; future media bytes are outside that
commitment. `caller_public_key`, when non-empty, requires the
`Livepeer-Caller-Proof` defined by the header contract. An empty key selects
exact-scope bearer semantics.

An authorization applies to one payee, broker route, capability, offering,
protocol, quote, work-unit curve, and cumulative maximum. It MUST NOT be
redirected or broadened.

## 3. Funding

`Livepeer-Payment` is optional on an account-authorized invocation. When
present, the receiver validates it first and credits its EV to the stable
account. It then atomically reserves the authorization maximum. When absent,
the reservation uses existing available account value.

The sender SHOULD maintain a bounded target float. Given a trusted account
observation:

```text
shortfall = max(0, target_available - observed_available)
```

It mints tickets only for `shortfall`. Maximum workload units are not an
instruction to mint their entire value.

`POST /v1/payment/account/fund` accepts a `Livepeer-Payment` plus the exact
capability/offering headers whose price is signed in that payment. It credits
the stable account and admits no work, so an in-path provider or out-of-path
funding intermediary can replenish aggregate float without possessing an end
user's session credential. Replaying the same ticket batch returns the
recorded account without crediting it twice.

The mint intent is idempotent independently of the workload authorization.
If a receiver credits a ticket and crashes before moving the credited
generation balance into the stable account, replay may report
`NONCE_REPLAY`; the receiver MUST recover the already-credited generation
balance rather than require a replacement ticket. A payment sender different
from the authorization payer MUST be refused.

## 4. States and idempotency

```text
issued -> admitted -> settled
   |          \----> outcome_unknown
   \-> expired_unused
```

`authorization_id` is payer-scoped and idempotent. Reuse with different
content MUST fail. Repeated admission or settlement with identical content
MUST replay the recorded result without a second reservation, execution, or
debit.

The receiver may transition an unadmitted authorization to `expired_unused`
after `expires_at`. That transition is irrevocable: it MUST refuse every later
admission even if clocks or policy change. Only this terminal state authorizes
the payer to release a non-admission hold automatically.

## 5. Settlement and callback independence

The broker settles actual measured units up to the signed cumulative cap and
releases the difference. Work reported beyond that cap is seller risk and MUST
NOT create involuntary payer credit. The signed record keeps measured units,
billed units, account funding, reservation, debit, release, and account
version distinct.

The broker MUST retain signed, payer-queryable terminal evidence for the
protocol retention window. `GET /v1/exchange/{request_id}` is authoritative
for jobs; the paid-session status lookup is authoritative for sessions. After
authorization expiry, the existing signed non-admission endpoint can prove
that a request was never admitted; session opens and jobs share its durable
admission tombstone and cannot later contradict that evidence. SDK delivery of
settlement to a payer is a latency optimization, not a correctness dependency.

A payer MAY reconcile exclusively by querying the locked broker using its own
request or session ID. Raw HTTP and SDK callers have identical financial
semantics.

## 6. Sessions

A session authorization carries a cumulative maximum. Admission reserves only
a bounded initial runway. `AdvanceAuthorization` atomically advances the
cumulative billing curve and replaces the prior runway reservation; optional
funding on that call credits only aggregate account shortfall. Extensible
sessions increase their cap using a new authorization revision bound to the
same session and predecessor; bounded sessions cannot increase it.

Account replenishment is aggregate across authorized engagements. A broker
MUST stop or wind down when funded, authorized runway is exhausted; it MUST NOT
extend involuntary payer credit.

The receiver exposes account and authorization views through its local gRPC
contract. The reference broker relays those views at
`POST /v1/payment/account`. This TLS-bound observation is suitable for
shortfall calculation and operational reconciliation but is not terminal
settlement evidence; terminal financial decisions use the broker-signed
records above. A caller MUST NOT accept an observation from a different route.

## 7. Compatibility

Account-backed authorization is intrinsic to `paid-job/v1` and
`paid-session/v1`; it is not an offering feature and MUST NOT be negotiated
through `extra.features.wholesale_accounts` or any equivalent flag. Every paid
workload invocation requires `Livepeer-Authorization`. A payment-only workload
request MUST fail closed without crediting its tickets or running work.

`Livepeer-Payment` remains valid only at `POST /v1/payment/account/fund` or as
optional shortfall funding alongside a valid authorization. Mixed-version
peers fail closed. Implementations MUST NOT silently fall back to a
ticket-session workload path.

## 8. Conformance

Conformance covers: funding-free admission from existing credit, exact
shortfall funding, concurrent reservation exclusion, replay, altered scope,
expiry, omitted callback recovery, actual settlement and release, session cap
revision, recipient-random rotation, and restart durability.

## 9. Migration and drain

This change is a coordinated hard cut, not a per-offer or per-payer opt-in.
Before upgrading, operators MUST stop new payment-only admissions and drain all
legacy in-flight jobs, session debits, and accounting retries to durable
outcomes. Existing sessions are not upgraded by inventing authorization state.

After the drain, verified residual generation balances move into the stable
account exactly once. Each source generation is atomically marked migrated so
restart or retry cannot double-credit it. All brokers, payment daemons, payer
services, and conformance clients on a route then upgrade together; older peers
receive an explicit authorization-required refusal.

Removing a route does not erase service credit. Payers SHOULD stop new funding,
stop assigning new work, allow admitted work to settle, and consume the
remaining available credit before deselection. Because probabilistic-ticket EV
is a service-credit accounting quantity rather than refundable escrow, v1 does
not promise cash withdrawal or cross-payee transfer; any bilateral refund is an
explicit adjustment outside this protocol. Target float bounds the maximum
route-exit residue rather than allowing it to grow with every request maximum.

## Changelog

| Version | Date | Change |
|---|---|---|
| 1.1.0-draft | 2026-09-11 | Makes stable wholesale accounts and single-purpose spend authorization mandatory for every paid workload. Removes offer feature negotiation and payment-only fallback, defines the coordinated drain, and confines tickets to account funding and authorized shortfall funding. |
| 1.0.0-draft | 2026-09-09 | Introduces stable payer-payee accounts, single-purpose spend authorization, aggregate shortfall funding, and migration-fenced compatibility with ticket-session accounting. |
