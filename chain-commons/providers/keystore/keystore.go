// Package keystore provides the signing abstraction.
//
// The interface is HSM-shaped (Sign / SignTx / Address) so HSM/KMS impls
// can land later without changing service code. v1 ships only V3 JSON
// keystore; HSM is tracked in tech-debt.
package keystore

import (
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/ethereum/go-ethereum/core/types"
)

// Keystore is the signing abstraction. Implementations may hold the key
// in process memory (V3 JSON), in an HSM (future), or in a remote KMS
// (future). Service code only sees the methods below.
//
// Keystore is intentionally minimal — every implementation supports it.
// Implementations that can also produce raw (non-EIP-191) signatures
// additionally satisfy RawSigner; consumers that need that capability
// type-assert at the boundary.
type Keystore interface {
	// Address returns the eth address derived from the key.
	Address() chain.Address

	// Sign produces an EIP-191 personal-sign signature over payload.
	Sign(payload []byte) ([]byte, error)

	// SignTx signs the given transaction using EIP-1559 (or legacy if the
	// tx type is legacy) for the given chain ID.
	SignTx(tx *types.Transaction, chainID chain.ChainID) (*types.Transaction, error)
}

// RawSigner is an optional capability for Keystore implementations that
// can produce signatures over keccak256(payload) directly without the
// EIP-191 personal-sign prefix.
//
// NOTE: this is NOT the format used by Livepeer's pm-ticket signing
// protocol — that protocol uses EIP-191 + v ∈ {27, 28} (see
// go-livepeer/eth/accountmanager.Sign + pm/sigverifier.go, and the
// canonical wire-compat reference in
// payment-daemon/docs/design-docs/wire-compat.md). Consumers signing
// pm tickets MUST use the standard EIP-191 `Sign` method, not RawSign.
//
// RawSigner is intended for callers that genuinely need raw output
// (e.g., signing a pre-hashed payload destined for a non-personal-sign
// use case). Type-assert at the boundary:
//
//	if rs, ok := ks.(keystore.RawSigner); ok {
//	    sig, err := rs.RawSign(somePreHashedPayload)
//	}
//
// HSM/KMS implementations that don't expose raw signing will not
// satisfy this interface; that's fine for any pm-ticket-signing
// workload (which doesn't need RawSign anyway).
type RawSigner interface {
	// RawSign produces an ECDSA signature over keccak256(payload) directly,
	// without the EIP-191 prefix. Returns the [R || S || V] 65-byte form
	// produced by go-ethereum's crypto.Sign.
	RawSign(payload []byte) ([]byte, error)
}
