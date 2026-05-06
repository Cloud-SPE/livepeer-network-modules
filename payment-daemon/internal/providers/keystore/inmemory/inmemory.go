// Package inmemory is the V3-keystore-backed implementation of
// providers.KeyStore. It holds a decrypted ECDSA private key in memory
// and signs ticket hashes with it, producing 65-byte EIP-191
// `personal_sign` signatures (`[R || S || V]` with V ∈ {27, 28}).
//
// It pairs with `keystore/jsonfile.Load` — the loader unlocks the V3
// JSON file, the constructor here takes ownership of the resulting
// *ecdsa.PrivateKey for the daemon's lifetime. The decrypted key is
// never logged, never marshalled, and never serialized; the struct's
// String() method is overridden to redact it.
//
// Source: ported from
// `livepeer-modules-project/payment-daemon/internal/providers/keystore/inmemory/inmemory.go`
// at tag v4.1.3 (SHA caddeb342edb88faeea6a52e83a24c55704f0ef5). Adapted
// to the local providers.KeyStore signature
// (`Address() []byte`, `Sign(hash []byte) ([]byte, error)`) — the prior
// impl used `(context.Context, []byte)` and `ethcommon.Address`. Per
// AGENTS.md lines 62-66 the port is a deliberate carryover; the source
// path and tag are recorded in the introducing commit.
package inmemory

import (
	"crypto/ecdsa"
	"errors"
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum/accounts"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// ErrNilKey indicates the KeyStore was constructed with a nil private key.
var ErrNilKey = errors.New("inmemory keystore: nil private key")

// KeyStore holds an ECDSA private key and signs arbitrary hashes with
// it. Implements providers.KeyStore.
type KeyStore struct {
	mu   sync.RWMutex
	key  *ecdsa.PrivateKey
	addr [20]byte // ETH address bytes (keccak256 of pubkey, last 20 bytes)
}

// New builds a KeyStore from an already-decrypted ECDSA key. The
// caller surrenders ownership of `key` — we hold the reference for the
// daemon's lifetime, never serialize it, never log it.
func New(key *ecdsa.PrivateKey) (*KeyStore, error) {
	if key == nil {
		return nil, ErrNilKey
	}
	addr := crypto.PubkeyToAddress(key.PublicKey)
	var raw [20]byte
	copy(raw[:], addr[:])
	return &KeyStore{
		key:  key,
		addr: raw,
	}, nil
}

// Address returns the 20-byte ETH address corresponding to the held
// key. Callers must not mutate the returned slice; we copy on return.
func (k *KeyStore) Address() []byte {
	k.mu.RLock()
	defer k.mu.RUnlock()
	out := make([]byte, 20)
	copy(out, k.addr[:])
	return out
}

// Sign returns an Ethereum personal_sign-style signature over `hash`:
//
//	digest = keccak256("\x19Ethereum Signed Message:\n" + len(hash) + hash)
//	sig    = ECDSA_sign(digest, key)
//	sig[64] += 27         // v ∈ {27, 28}
//
// This mirrors go-livepeer's `eth.accountManager.Sign` so signatures
// produced here verify against pm's DefaultSigVerifier. Output is a
// 65-byte `[R || S || V]` with V ∈ {27, 28}, per the providers.KeyStore
// interface contract.
func (k *KeyStore) Sign(hash []byte) ([]byte, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.key == nil {
		return nil, ErrNilKey
	}
	digest := accounts.TextHash(hash)
	sig, err := crypto.Sign(digest, k.key)
	if err != nil {
		return nil, err
	}
	// crypto.Sign returns V ∈ {0, 1}. Ethereum personal_sign requires V ∈ {27, 28}.
	sig[64] += 27
	return sig, nil
}

// SignTx implements providers.TxSigner. Signs an Ethereum transaction
// for the given chain ID using EIP-155 (legacy) signing — go-ethereum's
// `types.LatestSignerForChainID` resolves to the appropriate post-London
// signer when the tx itself carries dynamic-fee fields.
func (k *KeyStore) SignTx(tx *ethtypes.Transaction, chainID *big.Int) (*ethtypes.Transaction, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.key == nil {
		return nil, ErrNilKey
	}
	if chainID == nil {
		return nil, errors.New("inmemory keystore: nil chainID")
	}
	signer := ethtypes.LatestSignerForChainID(chainID)
	return ethtypes.SignTx(tx, signer, k.key)
}

// String redacts the key material so accidental logging does not leak
// the private key. The address is kept visible because it's public.
func (k *KeyStore) String() string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	addr := crypto.PubkeyToAddress(k.key.PublicKey)
	return "inmemory.KeyStore{addr: " + addr.Hex() + ", key: <redacted>}"
}
