// Package v3json provides a V3 JSON keystore implementation of the
// keystore.Keystore interface.
//
// Reads and decrypts a single V3-format keystore file at startup, then
// keeps the unlocked private key in process memory for the daemon's
// lifetime. The file format is the standard go-ethereum / web3 V3 JSON
// keystore — operators may use any tooling that produces this format
// (geth, eth-cli, Foundry's `cast wallet`, MetaMask exports, etc).
//
// Future packages (e.g. providers/keystore/hsm) will land alongside this
// one when HSM/KMS is needed; the keystore.Keystore interface is HSM-shaped
// so consumers don't change.
package v3json

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"os"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	ksiface "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/keystore"
	ethkeystore "github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// Open reads a V3 JSON keystore file at path, decrypts it with password,
// and returns a Keystore. If expected is non-zero, the derived address must
// match — used to catch hot-wallet/cold-orchestrator configuration mistakes
// before the daemon starts signing transactions.
//
// Password is consumed only during this call; the returned Keystore holds
// the unlocked private key, never the password.
func Open(path, password string, expected chain.Address) (ksiface.Keystore, error) {
	if path == "" {
		return nil, errors.New("v3json: keystore path is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("v3json: read keystore file: %w", err)
	}
	key, err := ethkeystore.DecryptKey(raw, password)
	if err != nil {
		return nil, fmt.Errorf("v3json: decrypt keystore (likely bad password): %w", err)
	}
	addr := crypto.PubkeyToAddress(key.PrivateKey.PublicKey)
	if expected != (chain.Address{}) && addr != expected {
		return nil, fmt.Errorf("v3json: keystore address %s does not match expected %s", addr.Hex(), expected.Hex())
	}
	return &v3Keystore{priv: key.PrivateKey, address: addr}, nil
}

type v3Keystore struct {
	priv    *ecdsa.PrivateKey
	address chain.Address
}

// Address implements keystore.Keystore.
func (k *v3Keystore) Address() chain.Address { return k.address }

// Sign implements keystore.Keystore. Produces an EIP-191 personal-sign
// signature over payload — the standard format for off-chain manifest
// signing and similar use cases.
func (k *v3Keystore) Sign(payload []byte) ([]byte, error) {
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(payload))
	hash := crypto.Keccak256([]byte(prefix), payload)
	return crypto.Sign(hash, k.priv)
}

// RawSign implements keystore.Keystore. Produces a raw ECDSA signature
// over keccak256(payload), with v ∈ {0, 1}.
//
// NOTE: this is NOT the format used by Livepeer's pm-ticket signing
// protocol — that protocol uses EIP-191 personal_sign with v ∈ {27, 28}
// (see go-livepeer/eth/accountmanager.Sign + pm/sigverifier.go). The
// canonical wire-compat reference lives in
// payment-daemon/docs/design-docs/wire-compat.md.
//
// RawSign is provided as a primitive for callers that genuinely need
// raw output (e.g., signing a pre-hashed payload destined for a
// non-personal-sign use case). Do NOT use RawSign for pm tickets — use
// payment-daemon's chaincommonsadapter (which routes through the
// EIP-191 Sign path and normalizes v).
func (k *v3Keystore) RawSign(payload []byte) ([]byte, error) {
	hash := crypto.Keccak256(payload)
	return crypto.Sign(hash, k.priv)
}

// SignTx implements keystore.Keystore. Uses the latest go-ethereum signer
// for the configured chain ID — supports legacy and EIP-1559 transactions.
func (k *v3Keystore) SignTx(tx *types.Transaction, chainID chain.ChainID) (*types.Transaction, error) {
	signer := types.LatestSignerForChainID(chainID.BigInt())
	return types.SignTx(tx, signer, k.priv)
}

// Compile-time: v3Keystore satisfies both the minimal Keystore interface
// and the optional RawSigner capability.
var (
	_ ksiface.Keystore  = (*v3Keystore)(nil)
	_ ksiface.RawSigner = (*v3Keystore)(nil)
)
