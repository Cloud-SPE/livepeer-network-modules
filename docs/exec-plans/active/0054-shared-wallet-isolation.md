# Shared-wallet wholesale isolation

Bead: lnm-336. LOC and BlueClaw production and development share one on-chain wallet. Independent nonce allocators and application ledgers require separate identities.

The protocol adds required `wholesale_account_id` (1–128 ASCII letters, digits, `.`, `_`, `:`, `-`) to all account operations and signed evidence. Spend authorizations use domain `livepeer-spend-authorization/v3`. Each sender database creates and retains a random `ticket_stream_id`; receiver generation indexes bind payer, payee, route, account, and stream. Account and stream identifiers do not change the on-chain ticket format.

Funding uses the lowercase 64-character SHA-256 digest of the exact payment bytes as `funding_id`. A durable receipt records credited value and its original account snapshot; replay returns that same receipt, not a balance delta inferred from other operations. Nonce consumption, account credit, winner queue insertion, and receipt creation commit atomically. Inline funding uses the same receipt path.

The HTTP account/status JSON requires `wholesale_account_id`. Funding requires `Livepeer-Wholesale-Account-Id`. Ticket parameter requests and responses include account and stream identity; responses and account observations advertise `isolation_version: 1`. Missing or mismatched identities fail closed. Request and authorization IDs remain globally unique within a payer across broker workloads; account-scoped daemon ledgers do not promise reusable broker request IDs.

Migration requires quiescing workloads and settling existing authorizations using the old release before upgrading. Existing wallet-wide rows are retained untouched and cannot be reached by naming a new account. Operators must reconcile and drain legacy credit before cutover; this release does not automatically allocate old balances. No live deployment, image tag bump, contract upgrade, or central signer is part of this change. Shared keys and a common on-chain deposit remain a common trust and exposure boundary.
