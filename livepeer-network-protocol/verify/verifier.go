// Package verify recovers the Ethereum address that produced an
// EIP-191 personal-sign signature over canonical manifest bytes.
// Resolver, coordinator, and gateway all run this recovery to confirm
// the manifest was authored by the address that the chain trusts to
// set the orchestrator's serviceURI pointer.
//
// Ported with attribution from the prior reference impl
// service-registry-daemon/internal/providers/verifier/verifier.go;
// see secure-orch-console/AGENTS.md for the attribution record.
package verify

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
)

// EthAddress is held in canonical lower-case 0x-prefixed form. It
// duplicates secure-orch-console/internal/signing.EthAddress on
// purpose — the verify module is a leaf consumed by every component;
// adding a cross-component dep just for the address type is the wrong
// trade.
type EthAddress string

func (a EthAddress) String() string { return string(a) }

func (a EthAddress) Equal(b EthAddress) bool {
	return strings.EqualFold(string(a), string(b))
}

// Common errors. ErrSignatureMalformed signals a structural problem
// (wrong length); ErrSignatureMismatch signals a cryptographic failure
// (recovery returned no key, or recovered key differs from the
// expected address).
var (
	ErrInvalidEthAddress  = errors.New("verify: invalid eth address")
	ErrSignatureMalformed = errors.New("verify: signature malformed")
	ErrSignatureMismatch  = errors.New("verify: signature mismatch")
)

// Verifier recovers the eth address that signed canonical bytes.
type Verifier interface {
	Recover(canonical, signature []byte) (EthAddress, error)
}

// Secp256k1 is the production verifier — go-ethereum's recover under
// the EIP-191 personal-sign envelope.
type Secp256k1 struct{}

// New returns a default verifier.
func New() Verifier { return Secp256k1{} }

// Recover applies the personal-sign envelope and recovers the signer.
// Length and shape are validated; v in {27, 28} is normalized to the
// {0, 1} form go-ethereum's SigToPub expects.
func (Secp256k1) Recover(canonical, signature []byte) (EthAddress, error) {
	if len(signature) != 65 {
		return "", fmt.Errorf("%w: signature length %d != 65", ErrSignatureMalformed, len(signature))
	}
	sig := make([]byte, 65)
	copy(sig, signature)
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	digest := PersonalSignDigest(canonical)
	pub, err := crypto.SigToPub(digest, sig)
	if err != nil {
		return "", fmt.Errorf("%w: recover failed: %w", ErrSignatureMismatch, err)
	}
	if pub == nil {
		return "", fmt.Errorf("%w: nil public key recovered", ErrSignatureMismatch)
	}
	return parseEthAddress(crypto.PubkeyToAddress(*pub).Hex())
}

// PersonalSignDigest computes keccak256(prefix || canonical) per
// EIP-191. Symmetric with secure-orch-console/internal/signing's
// PersonalSignDigest; the round-trip test below pins them.
func PersonalSignDigest(canonical []byte) []byte {
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(canonical))
	return crypto.Keccak256([]byte(prefix), canonical)
}

func parseEthAddress(s string) (EthAddress, error) {
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
