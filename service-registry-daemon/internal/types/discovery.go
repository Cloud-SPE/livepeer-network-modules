package types

import (
	"time"
)

// Compatibility records evidence about the current manifest source, independently
// of retrieval availability and the eligibility of any cached publication.
type Compatibility string

const (
	CompatibilityUnknown      Compatibility = "unknown"
	CompatibilityVerified     Compatibility = "verified_compatible"
	CompatibilityIncompatible Compatibility = "confirmed_incompatible"
)

type FailureReason string

const (
	FailureNone          FailureReason = "none"
	FailureTransport     FailureReason = "transport"
	FailureHTTP          FailureReason = "http"
	FailureMissing       FailureReason = "manifest_missing"
	FailureUnsupported   FailureReason = "manifest_unsupported"
	FailureInvalid       FailureReason = "manifest_invalid"
	FailureSignature     FailureReason = "signature_invalid"
	FailureExpired       FailureReason = "manifest_expired"
	FailureReplay        FailureReason = "publication_replay"
	FailureChain         FailureReason = "chain_unavailable"
	FailureSourceMissing FailureReason = "source_missing"
	FailureInternal      FailureReason = "internal"
)

// DiscoveryStatus is also the durable retry record. Invalidated is sticky across
// transient failures/restarts and is cleared only by a newly accepted publication.
type DiscoveryStatus struct {
	SourceURI               string
	OverlayManifestURL      string
	SourceKnown             bool
	Compatibility           Compatibility
	Availability            string
	FailureReason           FailureReason
	ConsecutiveFailures     uint32
	PolicyStep              uint32
	RetryClass              string
	LastVerifiedAt          time.Time
	NextRetryAt             time.Time
	LastAttemptAt           time.Time
	CompatibilityCheckedAt  time.Time
	CompatibilityValidUntil time.Time
	SourceCheckedAt         time.Time
	Invalidated             bool
}

// ResolutionError supplies structured diagnostics without replacing the existing
// errors.Is contract. RetryAfter is measured at evaluation, not serialization.
type ResolutionError struct {
	Cause       error
	Address     EthAddress
	Status      DiscoveryStatus
	EvaluatedAt time.Time
	RetryAfter  time.Duration
}

func (e *ResolutionError) Error() string { return e.Cause.Error() }
func (e *ResolutionError) Unwrap() error { return e.Cause }

// FetchError preserves HTTP status rather than requiring error-string parsing.
type FetchError struct {
	Cause      error
	HTTPStatus int
}

func (e *FetchError) Error() string { return e.Cause.Error() }
func (e *FetchError) Unwrap() error { return e.Cause }
