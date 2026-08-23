// Package devkeystore is a dev-mode KeyStore. It holds a deterministic
// throwaway secp256k1 key rather than a generated one, so two daemons
// started with no override reach the same identity and a test can seed
// it — but the signatures it produces are REAL.
//
// It used to emit a synthetic SHA-256 vector with a hardcoded V=27, on
// the premise recorded in its own comment that "receivers in dev mode
// skip signature recovery". No such bypass exists: the receiver's
// validator always performs EIP-191 secp256k1 recovery. So a chain-free
// sender/receiver pair could not exchange a single payment — every
// ticket failed signature validation, which is exactly what the hermetic
// LOC matrix found.
//
// Making the key real is the right fix rather than teaching the receiver
// to skip validation in dev: a suite that bypasses the security boundary
// is not exercising it, and the one thing worth proving hermetically is
// that a tampered signature is refused.
//
// The key is still a throwaway with a published default. This is for
// dev and CI; it must never hold value.
package devkeystore

import (
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
)

// Default dev key (32 bytes). Override with --dev-signing-key-hex.
// A valid secp256k1 scalar: non-zero and below the curve order.
var defaultDevKey = []byte{
	0xde, 0xad, 0xbe, 0xef, 0xde, 0xad, 0xbe, 0xef,
	0xde, 0xad, 0xbe, 0xef, 0xde, 0xad, 0xbe, 0xef,
	0xde, 0xad, 0xbe, 0xef, 0xde, 0xad, 0xbe, 0xef,
	0xde, 0xad, 0xbe, 0xef, 0xde, 0xad, 0xbe, 0xef,
}

// DevKeyStore implements providers.KeyStore with a deterministic
// throwaway secp256k1 key.
type DevKeyStore struct {
	key  *ecdsa.PrivateKey
	addr []byte
}

// New constructs a DevKeyStore. If `keyHex` is empty, the package
// default key is used, so two daemons with no override share an
// identity. The address is the real keccak256-derived Ethereum address
// for the key — it has to be, because the receiver recovers a signer
// address from the signature and compares it to the sender's declared
// one.
func New(keyHex string) (*DevKeyStore, error) {
	key := defaultDevKey
	if keyHex != "" {
		raw, err := hex.DecodeString(keyHex)
		if err != nil {
			return nil, errors.New("--dev-signing-key-hex: invalid hex")
		}
		if len(raw) != 32 {
			return nil, errors.New("--dev-signing-key-hex: must be 32 bytes (64 hex chars)")
		}
		key = raw
	}
	// Rejects zero and anything at or above the curve order. Refusing
	// here beats signing with a key that cannot verify: the failure
	// would otherwise surface as an unexplained ticket rejection on the
	// far side of a daemon boundary.
	priv, err := crypto.ToECDSA(key)
	if err != nil {
		return nil, fmt.Errorf("--dev-signing-key-hex: not a valid secp256k1 key: %w", err)
	}
	addr := crypto.PubkeyToAddress(priv.PublicKey)
	return &DevKeyStore{
		key:  priv,
		addr: addr.Bytes(),
	}, nil
}

// Address returns the 20-byte Ethereum address for the held key.
func (k *DevKeyStore) Address() []byte {
	return append([]byte(nil), k.addr...)
}

// Sign returns an Ethereum personal_sign signature over `hash`,
// identical in construction to the production keystore:
//
//	digest = keccak256("\x19Ethereum Signed Message:\n" + len(hash) + hash)
//	sig    = ECDSA_sign(digest, key)
//	sig[64] += 27         // v ∈ {27, 28}
//
// Deterministic for a given (key, hash) because go-ethereum signs with
// RFC 6979, so dev tests can still pin values.
func (k *DevKeyStore) Sign(hash []byte) ([]byte, error) {
	if len(hash) == 0 {
		return nil, errors.New("devkeystore: hash is empty")
	}
	digest := accounts.TextHash(hash)
	sig, err := crypto.Sign(digest, k.key)
	if err != nil {
		return nil, err
	}
	// crypto.Sign returns V ∈ {0, 1}; personal_sign requires V ∈ {27, 28}.
	sig[64] += 27
	return sig, nil
}
