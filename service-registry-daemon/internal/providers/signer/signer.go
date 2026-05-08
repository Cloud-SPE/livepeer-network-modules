// Package signer holds the Signer provider — the operator's
// orchestrator-identity signing key. The default implementation
// decrypts a V3 JSON keystore at boot and signs canonical manifest
// bytes with eth-personal-sign.
//
// The Signer interface accepts opaque bytes so future providers
// (HSM/KMS/remote) plug in without changing service/.
package signer

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"os"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Signer signs canonical manifest bytes producing an Ethereum
// personal_sign signature. It also reports the address that signs.
type Signer interface {
	// Address returns the eth address of the signing key (lower-cased
	// 0x-prefixed). Stable across calls.
	Address() types.EthAddress

	// SignCanonical signs canonical bytes using eth-personal-sign:
	//   keccak256("\x19Ethereum Signed Message:\n" + len(canonical) + canonical)
	// Returns 65 bytes: r || s || v (v in {27, 28}).
	SignCanonical(canonical []byte) ([]byte, error)
}

// Keystore is the default Signer implementation: a V3 JSON keystore
// loaded into process memory. Closes by zeroing the in-memory key on
// Close().
type Keystore struct {
	priv *ecdsa.PrivateKey
	addr types.EthAddress
}

// LoadKeystore decrypts a V3 JSON keystore file into a Keystore signer.
func LoadKeystore(path, password string) (*Keystore, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is operator-supplied by design
	if err != nil {
		return nil, fmt.Errorf("signer: read keystore %s: %w", path, err)
	}
	key, err := keystore.DecryptKey(raw, password)
	if err != nil {
		return nil, fmt.Errorf("signer: decrypt keystore: %w", err)
	}
	addrStr := key.Address.Hex()
	addr, err := types.ParseEthAddress(addrStr)
	if err != nil {
		return nil, fmt.Errorf("signer: parse keystore address %q: %w", addrStr, err)
	}
	return &Keystore{priv: key.PrivateKey, addr: addr}, nil
}

// FromHexKey constructs a Keystore signer from a raw 0x-prefixed hex
// private key. Used by --dev mode and tests; not exposed in production
// flag surface.
func FromHexKey(hexKey string) (*Keystore, error) {
	key, err := crypto.HexToECDSA(strip0x(hexKey))
	if err != nil {
		return nil, fmt.Errorf("signer: parse hex key: %w", err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()
	parsed, err := types.ParseEthAddress(addr)
	if err != nil {
		return nil, err
	}
	return &Keystore{priv: key, addr: parsed}, nil
}

// GenerateRandom creates a fresh signer with a throwaway key. Used by
// --dev mode.
func GenerateRandom() (*Keystore, error) {
	key, err := crypto.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("signer: generate: %w", err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()
	parsed, err := types.ParseEthAddress(addr)
	if err != nil {
		return nil, err
	}
	return &Keystore{priv: key, addr: parsed}, nil
}

// Address returns the lower-cased 0x-prefixed eth address.
func (k *Keystore) Address() types.EthAddress {
	if k == nil || k.priv == nil {
		return ""
	}
	return k.addr
}

// SignCanonical applies the eth-personal-sign envelope and signs.
func (k *Keystore) SignCanonical(canonical []byte) ([]byte, error) {
	if k == nil || k.priv == nil {
		return nil, errors.New("signer: keystore not loaded")
	}
	digest := PersonalSignDigest(canonical)
	sig, err := crypto.Sign(digest, k.priv)
	if err != nil {
		return nil, fmt.Errorf("signer: sign: %w", err)
	}
	// crypto.Sign returns v in {0,1}; normalize to {27,28} per
	// Ethereum's personal_sign convention.
	if sig[64] < 27 {
		sig[64] += 27
	}
	return sig, nil
}

// Close zeros the private key bytes.
func (k *Keystore) Close() {
	if k == nil || k.priv == nil {
		return
	}
	// k.priv.D is *big.Int; we can't zero the underlying memory portably,
	// but we can drop the reference so a future GC pass collects it.
	k.priv = nil
}

// PersonalSignDigest computes keccak256(prefix || canonical) per
// Ethereum's personal_sign. Exposed so the verifier can reuse it.
func PersonalSignDigest(canonical []byte) []byte {
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(canonical))
	return crypto.Keccak256([]byte(prefix), canonical)
}

// Helpful exports for symmetric verifier construction.

// AddressFromHexKey is a small convenience for tests.
func AddressFromHexKey(hexKey string) (common.Address, error) {
	key, err := crypto.HexToECDSA(strip0x(hexKey))
	if err != nil {
		return common.Address{}, err
	}
	return crypto.PubkeyToAddress(key.PublicKey), nil
}

func strip0x(s string) string {
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		return s[2:]
	}
	return s
}
