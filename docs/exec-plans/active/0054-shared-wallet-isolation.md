# Shared-wallet wholesale isolation

Bead: lnm-336. LOC and BlueClaw production and development share one on-chain wallet. Independent nonce allocators and application ledgers require separate identities.

The protocol adds required `wholesale_account_id` (1–128 ASCII letters, digits, `.`, `_`, `:`, `-`) to all account operations and signed evidence. Spend authorizations use domain `livepeer-spend-authorization/v3`. Each sender database creates and retains a random `ticket_stream_id`; receiver generation indexes bind payer, payee, route, account, and stream. Account and stream identifiers do not change the on-chain ticket format.

Funding uses the lowercase 64-character SHA-256 digest of the exact payment bytes as `funding_id`. A durable receipt records credited value and its original account snapshot; replay returns that same receipt, not a balance delta inferred from other operations. Nonce consumption, account credit, winner queue insertion, and receipt creation commit atomically. Inline funding uses the same receipt path.

The HTTP account/status JSON requires `wholesale_account_id`. Funding requires `Livepeer-Wholesale-Account-Id`. Ticket parameter requests and responses include account and stream identity; responses and account observations advertise `isolation_version: 1`. Missing or mismatched identities fail closed. Request and authorization IDs remain globally unique within a payer across broker workloads; account-scoped daemon ledgers do not promise reusable broker request IDs.

Migration requires quiescing workloads and settling existing authorizations using the old release before upgrading. Existing wallet-wide rows are retained untouched and cannot be reached by naming a new account. Operators must reconcile and drain legacy credit before cutover; this release does not automatically allocate old balances. No live deployment, image tag bump, contract upgrade, or central signer is part of this change. Shared keys and a common on-chain deposit remain a common trust and exposure boundary.


## Integration with session recovery and concurrency

The current main recovery and concurrency changes retain account identity in
sealed initial-admission intents, durable open reservations, pending job
settlements, revision recovery, and terminal evidence. The receiver cancellation
RPC requires `wholesale_account_id`; its durable fence and subsequent status lookup
use the same payer/account/authorization key as admission. It returns an existing
admission unchanged, including cumulative billing. Signed session revision
evidence also includes the account ID (protobuf field 23).

The real-receiver recovery regression funds two accounts under one payer wallet
and admits the same receiver authorization ID in both. Lost initial-admission
responses, cancellation, usage reconciliation, receiver/broker restarts, and lost
settlement responses for one account must leave the other admitted and reserved.
The signed fixture includes the account in both revision and terminal envelopes.

This integration preserves the coordinated drain/cutover requirement above.
Broker request IDs remain globally unique within a payer. The separate paid-job
pre-admission intent and admission-before-capacity follow-ups remain tracked as
`lnm-q0ik` and `lnm-ffig`; this merge does not implement those redesigns.
