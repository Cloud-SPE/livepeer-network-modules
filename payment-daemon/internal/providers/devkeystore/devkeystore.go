// Package devkeystore is a dev-mode KeyStore. It signs nothing real —
// returns a deterministic 65-byte vector built from `keccak256(devKey ||
// hash)` — and exposes a fake ETH address.
//
// This is NOT cryptographically valid for on-chain redemption. Plan
// 0016 swaps in V3 JSON keystore loading + go-ethereum signing.
package devkeystore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// Default dev key (32 bytes). Override with --dev-signing-key-hex.
var defaultDevKey = []byte{
	0xde, 0xad, 0xbe, 0xef, 0xde, 0xad, 0xbe, 0xef,
	0xde, 0xad, 0xbe, 0xef, 0xde, 0xad, 0xbe, 0xef,
	0xde, 0xad, 0xbe, 0xef, 0xde, 0xad, 0xbe, 0xef,
	0xde, 0xad, 0xbe, 0xef, 0xde, 0xad, 0xbe, 0xef,
}

// DevKeyStore implements providers.KeyStore with a deterministic
// stand-in for ECDSA signing.
type DevKeyStore struct {
	key  []byte
	addr []byte
}

// New constructs a DevKeyStore. If `keyHex` is empty, the package
// default key is used (so two daemons with no override produce
// reproducible identities). The derived address is `last 20 bytes of
// sha256(key)` — not a real keccak256-derived ETH address, but stable
// and easy to seed in tests.
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
	addrSum := sha256.Sum256(key)
	return &DevKeyStore{
		key:  key,
		addr: addrSum[12:32], // last 20 bytes — placeholder address
	}, nil
}

// Address returns the deterministic 20-byte stand-in address.
func (k *DevKeyStore) Address() []byte {
	return append([]byte(nil), k.addr...)
}

// Sign returns 65 bytes built from `sha256(key || personalPrefix(hash))`,
// padded out with a fake `V = 27`. Not a real ECDSA signature; receivers
// in dev mode skip signature recovery.
//
// The output is byte-stable for a given (key, hash) pair so dev tests
// can pin expected values.
func (k *DevKeyStore) Sign(hash []byte) ([]byte, error) {
	if len(hash) == 0 {
		return nil, errors.New("devkeystore: hash is empty")
	}
	prefix := []byte("\x19Ethereum Signed Message:\n32")
	digest := sha256.New()
	_, _ = digest.Write(k.key)
	_, _ = digest.Write(prefix)
	_, _ = digest.Write(hash)
	r := digest.Sum(nil) // 32 bytes
	digest.Reset()
	_, _ = digest.Write(r)
	_, _ = digest.Write(k.key)
	s := digest.Sum(nil) // 32 bytes

	out := make([]byte, 0, 65)
	out = append(out, r...)
	out = append(out, s...)
	out = append(out, 27) // V ∈ {27, 28}; pin to 27 in dev
	return out, nil
}
