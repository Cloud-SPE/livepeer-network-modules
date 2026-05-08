package types

import "errors"

// Sentinel errors for cross-layer comparisons. New error categories
// require a stable code string in product-specs/grpc-surface.md.
var (
	ErrNotFound             = errors.New("not_found")
	ErrManifestUnavailable  = errors.New("manifest_unavailable")
	ErrSignatureMismatch    = errors.New("signature_mismatch")
	ErrParse                = errors.New("parse_error")
	ErrUnknownField         = errors.New("unknown_field")
	ErrManifestTooLarge     = errors.New("manifest_too_large")
	ErrChainUnavailable     = errors.New("chain_unavailable")
	ErrUnknownMode          = errors.New("unknown_mode")
	ErrCacheStaleFailing    = errors.New("cache_stale_failing")
	ErrKeystoreLocked       = errors.New("keystore_locked")
	ErrChainWriteFailed     = errors.New("chain_write_failed")
	ErrChainWriteNotImpl    = errors.New("chain_write_not_implemented")
	ErrInvalidEthAddress    = errors.New("invalid_eth_address")
	ErrInvalidSchemaVersion = errors.New("invalid_schema_version")
	ErrEmptyNodes           = errors.New("empty_nodes")
	ErrInvalidNodeURL       = errors.New("invalid_node_url")
	ErrSignatureMalformed   = errors.New("signature_malformed")
	ErrManifestExpired      = errors.New("manifest_expired")
)

// ManifestValidationError wraps a sentinel with an optional field-path
// detail. The field-path lets a consumer point an operator at the exact
// JSON location that failed validation.
type ManifestValidationError struct {
	Cause error
	Field string // dot-path within the manifest, e.g. "nodes[2].url"
	Hint  string // human-readable remediation suggestion
}

func (e *ManifestValidationError) Error() string {
	switch {
	case e.Field != "" && e.Hint != "":
		return e.Cause.Error() + " at " + e.Field + ": " + e.Hint
	case e.Field != "":
		return e.Cause.Error() + " at " + e.Field
	case e.Hint != "":
		return e.Cause.Error() + ": " + e.Hint
	default:
		return e.Cause.Error()
	}
}

func (e *ManifestValidationError) Unwrap() error { return e.Cause }

// NewValidation is a small constructor for the most common shape.
func NewValidation(cause error, field, hint string) *ManifestValidationError {
	return &ManifestValidationError{Cause: cause, Field: field, Hint: hint}
}
