package resolver

import (
	"strings"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/utils"
)

// detectMode classifies an on-chain serviceURI value into one of the
// three resolver modes. See docs/design-docs/serviceuri-modes.md for
// the rules.
//
// The distinguishing test is comma-count:
//   - 0 commas → URL → WellKnown (manifest probe will decide if a
//     manifest is actually present; if not, caller falls back to Legacy).
//   - 2 commas (with the middle segment being a non-negative integer
//     and the last being base64-decodable) → CSV.
//   - anything else → Unknown (resolver returns ErrUnknownMode).
func detectMode(serviceURI string) types.ResolveMode {
	s := strings.TrimSpace(serviceURI)
	if s == "" {
		return types.ModeUnknown
	}
	commas := strings.Count(s, ",")
	switch commas {
	case 0:
		if utils.IsHTTPSURL(s) {
			return types.ModeWellKnown
		}
		return types.ModeUnknown
	case 2:
		// Confirm shape: <urlish>,<int>,<base64>
		parts := strings.SplitN(s, ",", 3)
		if !utils.IsHTTPSURL(parts[0]) {
			return types.ModeUnknown
		}
		if !isNonNegInt(parts[1]) {
			return types.ModeUnknown
		}
		if parts[2] == "" {
			return types.ModeUnknown
		}
		return types.ModeCSV
	default:
		// 1, 3+: ambiguous, refuse.
		return types.ModeUnknown
	}
}

func isNonNegInt(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
