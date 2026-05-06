// Package validator checks the shape and cryptographic integrity of a
// ticket received from a sender.
//
// Enforces invariants that are always checkable locally: address
// equality, recipient-rand preimage reveal, ECDSA sig recovery against
// the contract-defined ticket-hash flatten + EIP-191. IsWinningTicket
// evaluates the probabilistic winning predicate.
//
// Round / nonce / EV-cap policy lives in the receiver service; this
// package is stateless and side-effect-free.
package validator

import (
	"bytes"
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/types"
)

// Sentinel errors.
var (
	ErrInvalidRecipient     = errors.New("validator: invalid ticket recipient")
	ErrInvalidSender        = errors.New("validator: invalid ticket sender")
	ErrInvalidRecipientRand = errors.New("validator: invalid recipientRand for recipientRandHash")
	ErrInvalidSignature     = errors.New("validator: invalid ticket signature")
)

// Validate performs the full first-line ticket validation. Returns nil
// iff the ticket is acceptable for receiver-side processing.
func Validate(recipient []byte, ticket *types.Ticket, sig []byte, recipientRand *big.Int) error {
	if !bytes.Equal(ticket.Recipient, recipient) {
		return ErrInvalidRecipient
	}
	if isZeroAddr(ticket.Sender) {
		return ErrInvalidSender
	}
	if !bytes.Equal(types.HashRecipientRand(recipientRand), ticket.RecipientRandHash) {
		return ErrInvalidRecipientRand
	}
	if !verifySig(ticket.Sender, ticket.Hash(), sig) {
		return ErrInvalidSignature
	}
	return nil
}

// IsWinning reports whether the ticket is a winner. A ticket wins iff
// keccak256(sig || LeftPad(recipientRand, 32)) < ticket.WinProb.
func IsWinning(ticket *types.Ticket, sig []byte, recipientRand *big.Int) bool {
	return types.WinningHash(sig, recipientRand).Cmp(ticket.WinProb) < 0
}

// secp256k1halfN is (secp256k1_n - 1) / 2 — EIP-2 canonical-s upper
// bound. Higher s values indicate signature malleability and are
// rejected.
var secp256k1halfN = func() *big.Int {
	n, _ := new(big.Int).SetString("fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141", 16)
	return new(big.Int).Div(n, big.NewInt(2))
}()

// verifySig reports whether `sig` is a valid Ethereum personal_sign
// signature of `msg` by `addr`. Mirrors go-livepeer's
// crypto.VerifySig: 65-byte length, EIP-2 canonical-s, V ∈ {27, 28},
// EIP-191 digest before recovery.
func verifySig(addr, msg, sig []byte) bool {
	if len(sig) != 65 {
		return false
	}
	s := new(big.Int).SetBytes(sig[32:64])
	if s.Cmp(secp256k1halfN) > 0 {
		return false
	}
	if sig[64] != 27 && sig[64] != 28 {
		return false
	}
	ethSig := make([]byte, 65)
	copy(ethSig, sig)
	ethSig[64] -= 27

	digest := accounts.TextHash(msg)
	pub, err := crypto.SigToPub(digest, ethSig)
	if err != nil {
		return false
	}
	recovered := crypto.PubkeyToAddress(*pub)
	return bytes.Equal(recovered.Bytes(), addr)
}

func isZeroAddr(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}
