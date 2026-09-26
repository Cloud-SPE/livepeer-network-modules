---
spec_name: wholesale-account
version: 3.0.0-draft
status: draft
last_updated: 2026-09-21
---

# Wholesale account and spend authorization

This specification separates probabilistic-ticket funding from permission to
run work. It is shared by `paid-job/v1` and `paid-session/v1`; it is not a
third interaction protocol and introduces no capability taxonomy.

The key words MUST, MUST NOT, SHOULD, and MAY are interpreted as in RFC 2119.

## 1. Stable account

The receiver maintains one economic account per `(chain, payer, payee,
settlement_domain_id, denomination, wholesale_account_id)`. Tickets credit this account. Requests, sessions, capabilities,
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


### 1.1 Settlement domains (protocol major 4)

`settlement_domain_id` MUST identify one independent financial ledger and MUST be
cold-bound to every route's `worker_url`. It MUST be a nonzero 256-bit public ID
encoded as `0x` plus 64 lowercase hex digits. The receiver owns and persists it;
ordinary broker configuration MUST NOT change it. Independent ledgers under the
same orchestrator MUST use distinct IDs. Account versions and balances are local
to their complete account tuple. Funding A MUST NOT credit B.

SelectedRoute, SpendAuthorizationPayload, AccountFundingIntent, account observations,
account RPC requests and SettlementRecord carry the ID. Missing or mismatching IDs
MUST fail closed before paid work or account mutation. Payers MUST verify an account
observation and ticket-parameter response name the selected route's domain before
using them. Ticket parameters carry the ID in their HTTP response envelope; on-chain
ticket protobufs and redemption signatures are unchanged.

A URL migration with the complete same ledger preserves account identity but requires
new route-bound authorizations. A new independent ledger starts a new account;
credit MUST NOT be silently merged or transferred. The exact canonical URI rules,
bootstrap/restore requirements and migration procedure are defined in
[settlement-domain identity](../../docs/design-docs/settlement-domain-identity.md).

### 1.2 Shared wallet, separate accounts and streams

`wholesale_account_id` is required and consists of 1–128 ASCII letters, digits,
`.`, `_`, `:`, or `-`. It identifies an application/environment's service credit;
for example `loc-prod` and `blueclaw-dev-alice`. There is no implicit default account.
Signed spend authorization, settlement, and non-admission payloads bind this field.
A shared private key remains one trust boundary; labels do not restrict a key holder
or isolate the common on-chain deposit from other applications' spending.

Recovery-capable clients persist the sender's `Health.ticket_stream_id` in
`CreatePayment.expected_ticket_stream_id` with their mint request. The sender
rejects a nonempty mismatch before reserving the mint ID or signing. Mint replay
is database-local: another sender sharing the wallet must not recover an uncertain
mint. Omission remains wire-compatible for older callers, without this guarantee.
Saved payment bytes can be replayed to funding independently of the minting sender.

Each independent sender database generates one persistent random `ticket_stream_id`.
Do not clone an active sender database into another concurrently running daemon.
Ticket generation identity is `(sender, recipient, capability, offering,
wholesale_account_id, ticket_stream_id)` within one settlement domain. The receiver
binds the account and stream before returning recipient-random parameters. Account
labels cannot redirect an already-issued generation. Independent streams may fund
the same account without sharing nonce allocation; the application's funding
coordination remains per account.

`POST /v1/payment/ticket-params` requires both fields and returns them with
`isolation_version: 1`. Senders MUST reject missing or mismatched echoes before
signing. `POST /v1/payment/account` requires `wholesale_account_id` alongside
`payer_eth_address` and optional `authorization_id`; account and authorization-status
responses echo the account, payer, payee, settlement domain, and `isolation_version: 1`.
Standalone funding requires `Livepeer-Wholesale-Account-Id`; its receipt likewise
returns `isolation_version: 1` and the account. Workload requests derive account
identity from their signed authorization, including inline funding.

## 2. Single-purpose authorization

`SpendAuthorization` is deterministic protobuf signed by the payer using
keccak256 plus Ethereum personal-sign (EIP-191). Its domain is exactly
`livepeer-spend-authorization/v3`.

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
Each funding operation is identified by `funding_id`, the lowercase 64-character
SHA-256 hex digest of the exact protobuf payment bytes (without `0x`). The
receiver MUST atomically persist accepted nonces, winning-ticket queue entries,
account credit, and its funding receipt. A batch containing any invalid or
already-consumed ticket MUST fail without partial credit or nonce consumption.
The receipt contains `wholesale_account_id`, the original `credited_value_wei`,
`available_value_wei`, and `account_version`. A retry MUST return the same original
amount and account snapshot with `replayed=true`, even after later account changes.
It MUST NOT return zero merely because the batch was previously accepted, sweep
another batch's generation balance, or reconstruct a receipt from account deltas.
Changing the bytes of an already-consumed ticket batch does not create new credit.
Standalone and inline funding use the same receipt ledger. A payment sender different
from the authorization payer or a generation bound to a different account MUST be
refused. Inline funding credit may persist even when subsequent admission fails;
retrying the exact payment recovers its receipt without charging twice.

## 4. States and idempotency

```text
issued -> admitted -> settled
   |          \----> outcome_unknown
   \-> expired_unused
```

Receivers scope authorization state to the complete account tuple. Callers MUST
generate globally unique `authorization_id`, request IDs, and gateway session IDs
across accounts on a broker (for example UUIDs): broker workload indexes do not
promise reuse of those IDs between accounts. Reuse with different
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

A broker job-idempotency record alone does not establish receiver admission.
The broker MAY retain a distinct `ADMISSION_REJECTED` diagnostic outcome for a
definitive insufficient-balance refusal before runner execution. This outcome and
HTTP 402 are not refund evidence. After expiry, signed non-admission requires
matching the complete scope to the retained authorization and irrevocably fencing
that exact unused authorization at the receiver, then atomically persisting proof
against the durable no-execution state. Failed fencing or ambiguous transport
results MUST NOT produce non-admission evidence. Replays MUST NOT re-enter payment
or execution. Existing records without definitive refusal evidence remain unknown.
Quote version zero is valid; a missing query version is not equivalent to an
explicit zero. Signed protobuf JSON may omit a zero scalar.

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
Contribution receipt producers must preserve the inherited billed-value and
usage baseline: the successor contributes only new billing above that baseline,
never the cumulative value already attributed to its predecessor.

Accepted advances retain immutable per-sequence billing evidence. An exact
replay returns the historical accepted cumulative billed value with no new
funding or debit, including after expiry, supersession or final settlement.
Changed units, reservation or identities are rejected. This historical result
does not authorize new execution; current balances remain current. A legacy
sequence without retained evidence must not be reconstructed by guessing.

Account replenishment is aggregate across authorized engagements. A broker
MUST stop or wind down when funded, authorized runway is exhausted; it MUST NOT
extend involuntary payer credit.

The receiver exposes account and authorization views through its local gRPC
contract. The reference broker relays those views at
`POST /v1/payment/account`. This TLS-bound observation is suitable for
shortfall calculation and operational reconciliation but is not terminal
settlement evidence; terminal financial decisions use the broker-signed
records above. A caller MUST NOT accept an observation from a different route.

An account observation does not reserve funds. Consumers sharing a payer account
MUST coordinate observation and funding preparation until the preceding admission
is visible at that receiver (or signed terminal evidence has been reconciled).
This coordination must survive client cancellation/restart and cover every replica
that can prepare funding for the same domain/payer/payee/account tuple. It need not wait
for inference completion. An unknown admission must not silently release the fence
or cause a duplicate paid invocation. TLS account views remain unsuitable for
customer refunds.

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

### 7.1 Coordinated shared-wallet cutover

Stop new paid work and funding, settle or reconcile every active authorization, and
drain/reconcile the old wallet-wide service credit using the old release before
upgrading. Existing legacy rows MUST remain intact. Upgrading MUST NOT assign their
balances or authorization history to a newly named account. New named accounts
start with zero credit. This release has no automatic reallocation or legacy-drain
RPC; retain a ledger backup and complete the drain before cutover. All serving
brokers, receiver daemons, payer daemons and consuming applications upgrade together;
older peers lacking identity echoes fail closed.

`PayeeAdmin.ResetSession` requires the specific account and ticket stream; it retires
only that generation. After an operator reset, senders may need explicit
`ReportPaymentResult(INVALID_RECIPIENT_RAND)` or a restart to invalidate cached
parameters. Never remint an uncertain payment merely because a response was lost:
first replay the exact funding bytes to recover its durable receipt.

## 8. Conformance

The [shared-wallet executable fixture](../../payment-daemon/internal/service/sender/isolation_e2e_test.go)
requires independent daemon databases with the same wallet to start at nonce one
on distinct generations, concurrently fund, reject cross-account funding and debit,
and replay immutable funding receipts after later funding/debit. The
[store restart fixture](../../payment-daemon/internal/store/isolation_test.go) checks
atomic rollback, durable stream and receipt identity, and retention of legacy credit.

Conformance additionally requires two independent receiver ledgers with one payer
and payee: funding A leaves B unchanged; versions advance independently; B rejects
A authorizations and A tickets; both balances can fund work; a URL move preserves
one ledger while a new ledger starts a new account. See the executable
[two-ledger fixture](../../payment-daemon/internal/service/sender/settlement_domain_e2e_test.go)
and [store migration tests](../../payment-daemon/internal/store/settlement_domain_test.go).

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
| 3.0.0-draft | 2026-09-21 | Requires explicit wholesale accounts, persistent independent payer streams, v3 signed account binding, and atomic exact funding receipts. |
| 2.0.0-draft | 2026-09-14 | Requires immutable payment-ledger settlement domains, v2 signed authorizations, domain-bound funding/account observations, independent account versions and explicit migration semantics. |
| 1.1.0-draft | 2026-09-11 | Makes stable wholesale accounts and single-purpose spend authorization mandatory for every paid workload. Removes offer feature negotiation and payment-only fallback, defines the coordinated drain, and confines tickets to account funding and authorized shortfall funding. |
| 1.0.0-draft | 2026-09-09 | Introduces stable payer-payee accounts, single-purpose spend authorization, aggregate shortfall funding, and migration-fenced compatibility with ticket-session accounting. |

The private `CloseUnexecutedAuthorization` recovery operation requires durable
broker evidence that no runner binding was created. The receiver atomically
fences a missing authorization against late admission, or closes an admitted
authorization only when both accepted usage and billed value are zero. It must
reject recorded usage or billing. Expiry, timeouts and a transient NotFound
response do not establish this evidence. Bound work follows normal execution
and settlement recovery.
