# Rejected payment admission evidence

Implementing bead: `lnm-9da`; consumer coordination: BlueClaw `be-vtu`.

A job idempotency record precedes receiver admission. Treating every such record
as proof of admission strands definitively rejected payments. Removing the record
would permit re-execution and contradict later refund evidence.

Retain the exact authorization only when the receiver returns its definitive
insufficient-account-balance refusal, before entering the runner. Persist a
`payment_rejected` terminal state while retaining the request tombstone. Other
errors, including lost responses, remain ambiguous. Replay cannot enter payment
or execution again.

After authorization expiry, compare the complete requested scope with the stored
authorization and canonical settlement domain. The private receiver recovery call
irrevocably closes that exact unused authorization. Only after it succeeds may a
single store transaction recheck the rejection/no-work state and persist signed
non-admission. An outage or scope mismatch produces no proof. Existing historical
records without this durable evidence remain conservative.

Validation uses the rejected-admission protocol fixture through actual HTTP
handlers, Bolt reopen/replay tests, race detection, and the existing broker suite.
The fixture includes quote version zero, which is a valid initial publication.
No new public endpoint, signature format, image tag, or dependency is required.

Implemented 2026-09-18. Docker `go test -race ./...` passed for the full broker;
protocol `go test ./...` and conformance passed (58 scenarios, zero failures or
skips). Source changes are ready for coordinated rollout; production was not
changed. Existing tracker lint/config warnings are outside this implementation.
