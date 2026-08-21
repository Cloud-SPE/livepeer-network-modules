// Package livepeerheader defines the canonical Livepeer-* HTTP header names
// and error codes used between gateway and broker, per the spec at
// livepeer-network-protocol/headers/livepeer-headers.md.
//
// HTTP headers are case-insensitive on the wire (RFC 7230); these constants
// are the canonical mixed-case form for emission. Read paths use http.Header
// which canonicalizes incoming keys.
package livepeerheader

// Required request headers (gateway → broker).
const (
	Capability = "Livepeer-Capability"
	Offering   = "Livepeer-Offering"
	Payment    = "Livepeer-Payment"
	// Protocol carries the protocol tag, e.g. "paid-job/v1". Replaces
	// the pre-v1 Livepeer-Mode + Livepeer-Spec-Version pair.
	Protocol = "Livepeer-Protocol"
	// RequestID is required on every paid request: it is the
	// idempotency key (paid-job §4, paid-session §3.1).
	RequestID = "Livepeer-Request-Id"
	// RebindFrom declares a recipient rotation on a session top-up: its
	// value is the work_id the session is moving OFF. Present only on
	// the retry after a rotation; a rebind is declared, never inferred
	// from a mismatched payment identity.
	RebindFrom = "Livepeer-Rebind-From"
)

// Response headers (broker → gateway).
const (
	Backoff      = "Livepeer-Backoff"
	WorkUnits    = "Livepeer-Work-Units"
	WorkUnitName = "Livepeer-Work-Unit"
	JobID        = "Livepeer-Job-Id"
	Settlement   = "Livepeer-Settlement"
	// NonAdmission carries a signed NonAdmissionRecord envelope. A
	// separate header from Settlement because the two are opposite
	// claims and a consumer must never treat one as the other.
	NonAdmission = "Livepeer-Non-Admission"
	HealthStatus = "Livepeer-Health-Status"
	Error        = "Livepeer-Error"
)

// Error codes that the Livepeer-Error response header may carry. See the
// spec's error-code table for HTTP-status mapping.
const (
	ErrCapabilityNotServed     = "capability_not_served"
	ErrOfferingNotServed       = "offering_not_served"
	ErrPaymentEnvelopeMismatch = "payment_envelope_mismatch"
	ErrPaymentInvalid          = "payment_invalid"
	ErrProtocolUnsupported     = "protocol_unsupported"
	ErrTransportUnsupported    = "protocol_transport_unsupported"
	ErrJobInFlight             = "job_in_flight"
	ErrRequestIDReuse          = "request_id_reuse"
	// ErrGatewaySessionIDReuse rejects an open whose gateway_session_id
	// is already bound to a retained session. The id is what a
	// clearinghouse looks a settlement up by, so it has to resolve to
	// exactly one session; accepting a duplicate would break that lookup
	// for both sessions, not just the second one.
	// ErrAccountingPending answers a settlement query for an exchange
	// that was delivered but whose debit has not landed and is still
	// being retried. Distinct from a job still running: nothing further
	// is expected from the backend, only from the ledger, and the
	// exchange will reach a terminal settlement either way. A consumer
	// holds the encumbrance rather than booking or writing it off.
	ErrAccountingPending = "accounting_pending"
	// ErrAdmitted refuses a non-admission query for a request the broker
	// does have a record of. The caller wanted evidence of absence and
	// the answer is presence, which is a different and better answer.
	ErrAdmitted = "admitted"
	// ErrCoverageGap refuses a non-admission query this broker cannot
	// honestly answer: its records begin after the job was issued, so
	// absence across the gap is forgetting rather than non-admission.
	ErrCoverageGap           = "coverage_gap"
	ErrGatewaySessionIDReuse = "gateway_session_id_reuse"
	// ErrAmbiguousIdentifier answers a settlement query whose key
	// matches more than one session — a work_id shared across sessions.
	// Returning one of them would be a valid signature for the wrong
	// session, so the query fails and names a key that resolves.
	ErrAmbiguousIdentifier = "ambiguous_identifier"
	ErrRefillRefused       = "refill_refused"
	// ErrRecipientRotated tells a gateway its payment identity is stale:
	// the payee rotated its recipient rand, so every ticket in the batch
	// was rejected. The remedy is mechanical — re-fetch ticket params,
	// re-mint, and retry declaring Livepeer-Rebind-From. A distinct code
	// exists so a gateway acts on a code instead of matching a message.
	ErrRecipientRotated = "recipient_rotated"
	// ErrRebindRefused reports a declared rotation rebind the broker
	// would not perform: the declared predecessor is not this session's
	// identity, the successor did not credit, or the sender differs.
	ErrRebindRefused      = "rebind_refused"
	ErrBackendUnavailable = "backend_unavailable"
	ErrCapacityExhausted  = "capacity_exhausted"
	ErrInternalError      = "internal_error"
	// ErrInsufficientBalance signals the broker terminated work because
	// PayeeDaemon.SufficientBalance reported the payer's balance no
	// longer covers the configured runway. Emitted as a Livepeer-Error
	// response, or as a trailer where the response is already in flight.
	ErrInsufficientBalance = "insufficient_balance"
)
