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
	Capability  = "Livepeer-Capability"
	Offering    = "Livepeer-Offering"
	Payment     = "Livepeer-Payment"
	SpecVersion = "Livepeer-Spec-Version"
	Mode        = "Livepeer-Mode"
)

// Optional request header (gateway → broker).
const (
	RequestID = "Livepeer-Request-Id"
)

// Response headers (broker → gateway).
const (
	Backoff      = "Livepeer-Backoff"
	WorkUnits    = "Livepeer-Work-Units"
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
	ErrSpecVersionUnsupported  = "spec_version_unsupported"
	ErrModeUnsupported         = "mode_unsupported"
	ErrBackendUnavailable      = "backend_unavailable"
	ErrCapacityExhausted       = "capacity_exhausted"
	ErrInternalError           = "internal_error"
	// ErrInsufficientBalance signals the broker terminated a long-running
	// session because PayeeDaemon.SufficientBalance returned false (plan
	// 0015). Emitted as a Livepeer-Error response or trailer; HTTP status
	// 402 (Payment Required) where the response is still in the
	// pre-handler phase, otherwise carried as a trailer where the
	// protocol allows it.
	ErrInsufficientBalance = "insufficient_balance"

	// rtmp-ingress-hls-egress error codes (plan 0011-followup). Added
	// at the end so concurrent additions from sibling plans append
	// cleanly above this comment block.
	ErrFFmpegSubprocessFailed = "ffmpeg_subprocess_failed"
	ErrRTMPIngestIdleTimeout  = "rtmp_ingest_idle_timeout"

	// session-control-plus-media error code: control-WS send buffer
	// stayed full beyond the configured drop window. Emitted as the
	// WebSocket close-frame reason and recorded in metrics.
	ErrBackpressureDrop = "backpressure_drop"
)

// ImplementedSpecVersion is the spec-wide major.minor this broker speaks.
// Receivers MUST validate the major component only; this constant exposes
// both for clarity in logs and diagnostic responses.
const ImplementedSpecVersion = "0.1"
