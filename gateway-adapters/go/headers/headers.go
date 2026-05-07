// Package headers carries the canonical Livepeer-* HTTP header names and
// machine-readable error codes for the Go half of gateway-adapters/.
//
// Mirrors the TypeScript half at ../../ts/src/headers.ts and the
// broker-side definitions at ../../../capability-broker/internal/livepeerheader/.
// When the spec at ../../../livepeer-network-protocol/headers/livepeer-headers.md
// changes, every mirror changes.
package headers

const (
	Capability   = "Livepeer-Capability"
	Offering     = "Livepeer-Offering"
	Payment      = "Livepeer-Payment"
	SpecVersion  = "Livepeer-Spec-Version"
	Mode         = "Livepeer-Mode"
	RequestID    = "Livepeer-Request-Id"
	Backoff      = "Livepeer-Backoff"
	WorkUnits    = "Livepeer-Work-Units"
	HealthStatus = "Livepeer-Health-Status"
	Error        = "Livepeer-Error"
)

const (
	ErrCapabilityNotServed     = "capability_not_served"
	ErrOfferingNotServed       = "offering_not_served"
	ErrPaymentEnvelopeMismatch = "payment_envelope_mismatch"
	ErrPaymentInvalid          = "payment_invalid"
	ErrSpecVersionUnsupported  = "spec_version_unsupported"
	ErrModeUnsupported         = "mode_unsupported"
	ErrBackendUnavailable      = "backend_unavailable"
	ErrCapacityExhausted       = "capacity_exhausted"
	ErrInternalError           = "internal_error"
)

const SpecVersionValue = "0.1"
