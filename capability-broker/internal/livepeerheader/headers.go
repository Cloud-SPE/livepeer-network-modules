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
)

// Response headers (broker → gateway).
const (
	Backoff      = "Livepeer-Backoff"
	WorkUnits    = "Livepeer-Work-Units"
	WorkUnitName = "Livepeer-Work-Unit"
	JobID        = "Livepeer-Job-Id"
	Settlement   = "Livepeer-Settlement"
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
	ErrRefillRefused           = "refill_refused"
	ErrBackendUnavailable      = "backend_unavailable"
	ErrCapacityExhausted       = "capacity_exhausted"
	ErrInternalError           = "internal_error"
	// ErrInsufficientBalance signals the broker terminated work because
	// PayeeDaemon.SufficientBalance reported the payer's balance no
	// longer covers the configured runway. Emitted as a Livepeer-Error
	// response, or as a trailer where the response is already in flight.
	ErrInsufficientBalance = "insufficient_balance"
)
