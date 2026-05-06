// Package signing produces secp256k1 + EIP-191 personal-sign
// signatures over canonical manifest bytes. The Signer interface
// accepts opaque bytes so future providers (YubiHSM 2 PKCS#11 in
// commit 6, Ledger later) plug in without changing the console
// surface.
//
// Ported with attribution from the prior reference impl
// service-registry-daemon/internal/providers/signer/signer.go;
// see secure-orch-console/AGENTS.md for the attribution record.
package signing

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"os"

	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/crypto"
)

// Signer signs canonical manifest bytes and reports the address that
// signs.
type Signer interface {
	Address() EthAddress

	// SignCanonical signs canonical bytes using EIP-191 personal-sign:
	//   keccak256("\x19Ethereum Signed Message:\n" + len(canonical) + canonical)
	// Returns 65 bytes: r || s || v with v in {27, 28}.
	SignCanonical(canonical []byte) ([]byte, error)
}

// Keystore is a V3 JSON keystore loaded into process memory. It zeroes
// its private-key reference on Close so the GC can reclaim it.
type Keystore struct {
	priv *ecdsa.PrivateKey
	addr EthAddress
}

// LoadKeystore decrypts a V3 JSON keystore file.
func LoadKeystore(path, password string) (*Keystore, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is operator-supplied by design
	if err != nil {
		return nil, fmt.Errorf("signing: read keystore %s: %w", path, err)
	}
	key, err := keystore.DecryptKey(raw, password)
	if err != nil {
		return nil, fmt.Errorf("signing: decrypt keystore: %w", err)
	}
	addr, err := ParseEthAddress(key.Address.Hex())
	if err != nil {
		return nil, fmt.Errorf("signing: parse keystore address: %w", err)
	}
	return &Keystore{priv: key.PrivateKey, addr: addr}, nil
}

// FromHexKey constructs a Keystore from a 0x-prefixed hex private key.
// Used by tests and by the secure-orch-keygen helper after generation.
// Not exposed in the production console flag surface.
func FromHexKey(hexKey string) (*Keystore, error) {
	key, err := crypto.HexToECDSA(strip0x(hexKey))
	if err != nil {
		return nil, fmt.Errorf("signing: parse hex key: %w", err)
	}
	addr, err := ParseEthAddress(crypto.PubkeyToAddress(key.PublicKey).Hex())
	if err != nil {
		return nil, err
	}
	return &Keystore{priv: key, addr: addr}, nil
}

// GenerateRandom mints a fresh keypair. Used by the keygen helper and
// by tests; never as a default at console boot (the operator must
// supply the cold key).
func GenerateRandom() (*Keystore, error) {
	key, err := crypto.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("signing: generate: %w", err)
	}
	addr, err := ParseEthAddress(crypto.PubkeyToAddress(key.PublicKey).Hex())
	if err != nil {
		return nil, err
	}
	return &Keystore{priv: key, addr: addr}, nil
}

func (k *Keystore) Address() EthAddress {
	if k == nil || k.priv == nil {
		return ""
	}
	return k.addr
}

func (k *Keystore) SignCanonical(canonical []byte) ([]byte, error) {
	if k == nil || k.priv == nil {
		return nil, errors.New("signing: keystore not loaded")
	}
	digest := PersonalSignDigest(canonical)
	sig, err := crypto.Sign(digest, k.priv)
	if err != nil {
		return nil, fmt.Errorf("signing: sign: %w", err)
	}
	// crypto.Sign returns v in {0,1}; normalize to {27,28} per
	// Ethereum's personal_sign convention.
	if sig[64] < 27 {
		sig[64] += 27
	}
	return sig, nil
}

// Close drops the in-memory key reference so the GC can reclaim it.
// Best-effort: Go's runtime gives no guarantee the underlying big.Int
// memory is overwritten, but the reference is gone.
func (k *Keystore) Close() {
	if k == nil {
		return
	}
	k.priv = nil
}

// PersonalSignDigest computes keccak256(prefix || canonical) per
// EIP-191. Exposed so the verifier can reuse it.
func PersonalSignDigest(canonical []byte) []byte {
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(canonical))
	return crypto.Keccak256([]byte(prefix), canonical)
}

func strip0x(s string) string {
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		return s[2:]
	}
	return s
}
