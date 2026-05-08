package types

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// EthAddress is a 20-byte Ethereum address held in canonical lower-case
// 0x-prefixed string form. We deliberately use a string under the hood
// rather than [20]byte so that wire surfaces (gRPC, JSON, logs) can use
// the same value verbatim with no per-call hex conversion.
//
// All entry points that accept an address MUST run it through
// ParseEthAddress so the lower-cased canonical form is what the rest of
// the codebase manipulates. Equality compare with == on the string form.
type EthAddress string

// ParseEthAddress validates and canonicalizes an address string. It
// accepts mixed-case input (EIP-55 checksummed addresses are common in
// the wild) and returns the lower-cased form.
func ParseEthAddress(s string) (EthAddress, error) {
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return "", &ManifestValidationError{
			Cause: ErrInvalidEthAddress,
			Hint:  "address must be 0x-prefixed",
		}
	}
	body := s[2:]
	if len(body) != 40 {
		return "", &ManifestValidationError{
			Cause: ErrInvalidEthAddress,
			Hint:  fmt.Sprintf("address body must be 40 hex chars, got %d", len(body)),
		}
	}
	if _, err := hex.DecodeString(body); err != nil {
		return "", &ManifestValidationError{
			Cause: ErrInvalidEthAddress,
			Hint:  "address body must be valid hex",
		}
	}
	return EthAddress("0x" + strings.ToLower(body)), nil
}

// String is identity; defined for clarity at call sites.
func (a EthAddress) String() string { return string(a) }

// Equal compares two EthAddresses case-insensitively. Both sides should
// already be canonical from ParseEthAddress, but Equal is defensive.
func (a EthAddress) Equal(b EthAddress) bool {
	return strings.EqualFold(string(a), string(b))
}

// Bytes returns the 20-byte representation. Panics if the address is
// not canonical (which can only happen if a caller bypassed
// ParseEthAddress, a programming error). Callers in hot paths that need
// raw bytes should call ParseEthAddress + this once and cache the
// result.
func (a EthAddress) Bytes() []byte {
	b, err := hex.DecodeString(strings.TrimPrefix(strings.ToLower(string(a)), "0x"))
	if err != nil || len(b) != 20 {
		panic(fmt.Sprintf("non-canonical EthAddress reached Bytes(): %q", string(a)))
	}
	return b
}
