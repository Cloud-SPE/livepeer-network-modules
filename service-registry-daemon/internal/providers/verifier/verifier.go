// Package verifier holds the Verifier provider — recovery of the
// Ethereum address that produced an eth-personal-sign signature over a
// canonical-bytes input. The resolver uses this to confirm that the
// fetched manifest was authored by the address the chain trusts to set
// the orchestrator's serviceURI pointer.
package verifier

import (
	"errors"
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// Verifier recovers the eth address that signed canonical bytes.
type Verifier interface {
	Recover(canonical, signature []byte) (types.EthAddress, error)
}

// Secp256k1 is the production Verifier — go-ethereum's recover.
type Secp256k1 struct{}

// New returns a default Verifier.
func New() Verifier { return Secp256k1{} }

// personalSignDigest computes keccak256("\x19Ethereum Signed Message:\n" + len + canonical).
// Duplicated locally to avoid an import cycle with the signer package.
// Both forms must produce identical bytes; the round-trip test in
// signer_test.go enforces that invariant.
func personalSignDigest(canonical []byte) []byte {
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(canonical))
	return crypto.Keccak256([]byte(prefix), canonical)
}

// Recover applies the personal-sign envelope and recovers the signer.
func (Secp256k1) Recover(canonical, signature []byte) (types.EthAddress, error) {
	if len(signature) != 65 {
		return "", fmt.Errorf("%w: signature length %d != 65", types.ErrSignatureMalformed, len(signature))
	}
	// crypto.SigToPub expects v in {0,1}. Personal-sign signatures
	// usually carry v in {27,28}; normalize.
	sig := make([]byte, 65)
	copy(sig, signature)
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	digest := personalSignDigest(canonical)
	pub, err := crypto.SigToPub(digest, sig)
	if err != nil {
		return "", fmt.Errorf("%w: recover failed: %w", types.ErrSignatureMismatch, err)
	}
	if pub == nil {
		return "", errors.New("verifier: nil pub recovered")
	}
	addr := crypto.PubkeyToAddress(*pub).Hex()
	parsed, err := types.ParseEthAddress(addr)
	if err != nil {
		return "", err
	}
	return parsed, nil
}
