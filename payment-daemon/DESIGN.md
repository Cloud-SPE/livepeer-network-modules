# DESIGN — payment-daemon

## Why a separate process

`payment-daemon` exists to keep payment-session state, ticket signing,
ticket validation, and chain-facing redemption logic out of the broker
and the gateway. That separation is load-bearing for two reasons:

- the receiver-side daemon owns warm key material and the redemption
  pipeline
- the sender-side daemon owns the payer's nonce stream and session cache

Both sides need stable lifecycle and explicit contracts that survive
caller restarts and component extraction.

## Boundaries

- **Inbound, sender mode:** `PayerDaemon` gRPC over a unix socket. A
  sender-side client or the conformance runner calls `CreatePayment`,
  `ReportPaymentResult`, `GetDepositInfo`, and `Health`.
- **Inbound, receiver mode:** `PayeeDaemon` plus operator-only
  `PayeeAdmin` gRPC over a unix socket. The broker calls
  `GetTicketParams`, `OpenSession`, `ProcessPayment`, debit/balance
  methods, and `Health`. Operators use `PayeeAdmin.ResetSession`.
- **Outbound, sender mode:** HTTP `POST /v1/payment/ticket-params`
  against the selected broker URL to fetch authoritative payee-issued
  `TicketParams`.
- **Outbound, receiver mode:** optional Arbitrum JSON-RPC when
  `--chain-rpc` is set.
- **State:** BoltDB on both sides. The receiver keeps the session ledger;
  the sender keeps mint-idempotency records, so a retry after an
  uncertain response replays rather than signing a second batch. Only the
  sender's ticket-session cache is process-local memory — it is
  reconstructible from the payee, and losing it costs a round trip, not
  money.

## Load-bearing session contracts

### Sender-side minted-payment sessions

Sender mode caches sessions by stable route/funding identity:

- recipient
- capability
- offering
- funded value / target spend
- ticket-params base URL

Each cached session owns:

- the authoritative `TicketParams`
- the monotonic sender nonce stream
- the current `work_id = hex(recipient_rand_hash)`

`CreatePayment` returns that `work_id` to the caller.

### Receiver-side payee-issued sessions

Receiver mode persists a stable index keyed by:

- sender
- recipient
- capability
- offering

That index points to the currently-open `work_id`. The consequence is:

- repeated `GetTicketParams` for an open session return the same
  `recipient_rand_hash`
- daemon restart does not rotate the session
- explicit close/reset is what rotates session state

This is the contract the sender cache is built around.

## Rejection feedback loop

`ProcessPayment` is the payee-side truth source for ticket validity. It
returns:

- per-ticket `TicketStatus`
- `tickets_rejected`
- `dominant_rejection`

The important rejection for sender recovery is
`INVALID_RECIPIENT_RAND`.

Because `CreatePayment` and `ProcessPayment` happen in different
components, the sender cannot observe that rejection synchronously.
`PayerDaemon.ReportPaymentResult` closes that loop:

1. payee/broker observes `INVALID_RECIPIENT_RAND`
2. caller reports the outcome to the sender daemon
3. sender evicts the stale cached session
4. sender returns `codes.Aborted` with structured retry detail
5. caller retries exactly once and gets a fresh `work_id`

This avoids silent `work_id` swaps while keeping invalidation precise.

## Operator reset surface

Receiver mode exposes `PayeeAdmin.ResetSession(sender, recipient,
capability, offering)`.

Reset semantics:

- close the current open session for that stable identity
- delete the active stable-key index entry
- drop the old session's nonce ledger
- require the next `GetTicketParams` to mint a fresh `recipientRand`
  and `work_id`

This is the deliberate rotation mechanism. Restart is not.

## Storage model

Receiver-side BoltDB owns:

- session records keyed by `(sender, work_id)` or unsealed placeholders
- debit idempotency keys
- nonce ledger keyed by `(recipientRand, senderNonce)`
- redemption queue / redeemed-ticket metadata
- stable ticket-session index keyed by
  `(sender, recipient, capability, offering)`

The store package is the only owner of these buckets.

## Failure modes

| Surface | Failure | Expected behavior |
|---|---|---|
| `GetTicketParams` | repeated call for open session | same `work_id`, same `recipient_rand_hash` |
| `ProcessPayment` | bad signature / replay / stale rand | gRPC OK with structured rejection status; invalid tickets credit zero EV |
| `ReportPaymentResult` | `INVALID_RECIPIENT_RAND` | sender evicts cache and returns `codes.Aborted` with retry detail |
| `ResetSession` | missing / bad admin token | `PermissionDenied` |
| receiver restart | open session exists | stable session survives; next `GetTicketParams` reuses it |

## What is deliberately not here

- HTTP auth / operator UI around admin methods; today the guard is a
  bearer token on the unix-socket gRPC surface
- automatic sender-side retries inside `CreatePayment`; callers own the
  retry because `work_id` identity is caller-visible state
- persistent sender-side session cache; current design keeps sender
  state in memory and relies on explicit feedback for invalidation
