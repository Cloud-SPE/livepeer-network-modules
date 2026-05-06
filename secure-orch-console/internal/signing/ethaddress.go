package signing

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// EthAddress is held in canonical lower-case 0x-prefixed form so that
// wire surfaces, logs, and audit entries use the same value verbatim
// with no per-call hex conversion. Equality compares with == on the
// string form; callers MUST run untrusted input through ParseEthAddress
// before any equality check.
type EthAddress string

// ErrInvalidEthAddress flags malformed or non-hex address strings.
var ErrInvalidEthAddress = errors.New("invalid eth address")

func ParseEthAddress(s string) (EthAddress, error) {
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return "", fmt.Errorf("%w: must be 0x-prefixed", ErrInvalidEthAddress)
	}
	body := s[2:]
	if len(body) != 40 {
		return "", fmt.Errorf("%w: body must be 40 hex chars, got %d", ErrInvalidEthAddress, len(body))
	}
	if _, err := hex.DecodeString(body); err != nil {
		return "", fmt.Errorf("%w: body must be valid hex", ErrInvalidEthAddress)
	}
	return EthAddress("0x" + strings.ToLower(body)), nil
}

func (a EthAddress) String() string { return string(a) }

func (a EthAddress) Equal(b EthAddress) bool {
	return strings.EqualFold(string(a), string(b))
}
