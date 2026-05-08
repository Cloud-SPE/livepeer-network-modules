package chaintesting

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"fmt"
	"sync"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	ksiface "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/keystore"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// FakeKeystore is a deterministic Keystore for tests.
//
// Construct with NewFakeKeystore(seed) for a reproducible address derived
// from seed. The same seed always produces the same private key and
// address, so tests can hard-code expected addresses in fixtures.
type FakeKeystore struct {
	mu       sync.Mutex
	priv     *ecdsa.PrivateKey
	address  chain.Address
	signCnt  int
	failNext bool
	failErr  error
}

// NewFakeKeystore returns a FakeKeystore deterministically derived from seed.
// Different seeds produce different addresses; the same seed always produces
// the same address.
//
// The seed is hashed with SHA-256 then used as the private-key bytes; if the
// derived value is invalid for secp256k1 (vanishingly unlikely), the function
// panics — acceptable for tests.
func NewFakeKeystore(seed string) *FakeKeystore {
	h := sha256.Sum256([]byte(seed))
	priv, err := crypto.ToECDSA(h[:])
	if err != nil {
		panic(fmt.Sprintf("FakeKeystore: invalid derived key for seed %q: %v", seed, err))
	}
	return &FakeKeystore{
		priv:    priv,
		address: crypto.PubkeyToAddress(priv.PublicKey),
	}
}

// Address returns the eth address derived from the fake key.
func (k *FakeKeystore) Address() chain.Address {
	return k.address
}

// Sign produces an EIP-191 personal-sign signature over payload.
func (k *FakeKeystore) Sign(payload []byte) ([]byte, error) {
	k.mu.Lock()
	if k.failNext {
		k.failNext = false
		err := k.failErr
		k.mu.Unlock()
		return nil, err
	}
	k.signCnt++
	k.mu.Unlock()

	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(payload))
	hash := crypto.Keccak256([]byte(prefix), payload)
	return crypto.Sign(hash, k.priv)
}

// RawSign signs keccak256(payload) directly without the EIP-191 prefix.
// NOT for pm-ticket signing (which uses EIP-191 — see
// payment-daemon/docs/design-docs/wire-compat.md). Provided for
// callers that genuinely need a raw primitive over a pre-hashed payload.
// Same fail-injection semantics as Sign.
func (k *FakeKeystore) RawSign(payload []byte) ([]byte, error) {
	k.mu.Lock()
	if k.failNext {
		k.failNext = false
		err := k.failErr
		k.mu.Unlock()
		return nil, err
	}
	k.signCnt++
	k.mu.Unlock()

	hash := crypto.Keccak256(payload)
	return crypto.Sign(hash, k.priv)
}

// SignTx signs a transaction for the given chain ID. Uses EIP-1559 signer
// for typed transactions; falls back to LatestSigner otherwise.
func (k *FakeKeystore) SignTx(tx *types.Transaction, chainID chain.ChainID) (*types.Transaction, error) {
	k.mu.Lock()
	if k.failNext {
		k.failNext = false
		err := k.failErr
		k.mu.Unlock()
		return nil, err
	}
	k.signCnt++
	k.mu.Unlock()

	signer := types.LatestSignerForChainID(chainID.BigInt())
	return types.SignTx(tx, signer, k.priv)
}

// SignCount returns the total number of Sign + SignTx calls made.
func (k *FakeKeystore) SignCount() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.signCnt
}

// FailNextSign causes the next Sign or SignTx call to return err. Useful
// for testing keystore-failure handling.
func (k *FakeKeystore) FailNextSign(err error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.failNext = true
	k.failErr = err
}

// Compile-time: FakeKeystore satisfies both Keystore and the optional
// RawSigner capability.
var (
	_ ksiface.Keystore  = (*FakeKeystore)(nil)
	_ ksiface.RawSigner = (*FakeKeystore)(nil)
)
