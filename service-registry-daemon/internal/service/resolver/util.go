package resolver

import (
	"crypto/sha256"
	"errors"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

// sha256Sum returns the 32-byte SHA-256 of b. Local helper so the
// resolver doesn't expose crypto/sha256 across files.
func sha256Sum(b []byte) [32]byte { return sha256.Sum256(b) }

// modeLabel maps a domain ResolveMode to the metrics label string.
// Keeping the mapping local to the resolver because the metrics
// package can't import internal/types (it's a shared zero-dep
// provider) and types can't import metrics (layer rule).
func modeLabel(m types.ResolveMode) string {
	switch m {
	case types.ModeWellKnown:
		return metrics.ModeWellKnown
	case types.ModeCSV:
		return metrics.ModeCSV
	case types.ModeLegacy:
		return metrics.ModeLegacy
	case types.ModeStaticOverlay:
		return metrics.ModeStaticOverlay
	default:
		return metrics.ModeUnknown
	}
}

// legacyFallbackReason classifies the error that triggered a legacy
// synthesis. The resolver only falls back on a small set of errors;
// anything else surfaces. The resulting label is used as a counter
// dimension on legacy_fallbacks_total.
func legacyFallbackReason(err error) string {
	switch {
	case errors.Is(err, types.ErrManifestUnavailable):
		return "manifest_unavailable"
	case errors.Is(err, types.ErrManifestTooLarge):
		return "manifest_too_large"
	case errors.Is(err, types.ErrParse):
		return "parse_error"
	case errors.Is(err, types.ErrSignatureMismatch):
		return "signature_mismatch"
	default:
		return "other"
	}
}
