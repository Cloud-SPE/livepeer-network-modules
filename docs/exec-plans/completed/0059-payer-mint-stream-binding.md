# Payer mint recovery bound to durable stream identity

Tracking: `lnm-pm48`.

A shared wallet can have several independent sender databases. Mint IDs are
idempotent within a database, so recovering an uncertain mint through another
sender can sign a second payment. A gateway must persist the original stream
identity with its mint intent.

Expose the persistent sender `ticket_stream_id` in Health (field 3). Add optional
`expected_ticket_stream_id` to CreatePayment (field 7). Reject a nonempty mismatch
before reserving the mint ID or signing. Existing clients may omit the guard;
clients requiring durable cross-replica recovery must require a Health identity
and send the guard on every mint, including exact replay. Receiver Health leaves
the sender field empty. No wallet, account, signature or funding-ID format changes.

Validate using independent real sender databases with the same wallet: distinct
Health identities, wrong-owner rejection, then successful mint at the rejected
sender with its own guard proving rejection did not reserve the ID, and exact
replay at the original sender. Preserve the existing unguarded-client suite.
