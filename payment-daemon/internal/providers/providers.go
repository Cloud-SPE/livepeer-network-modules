// Package providers declares the abstractions that separate
// payment-daemon business logic from chain integration. v0.2 ships
// in-process fakes for every provider; plan 0016 swaps in real
// chain-backed implementations behind the same interfaces.
//
// The goal of this split is that everything in service/* depends only
// on these interfaces — chain integration is a swap, not a rewrite.
package providers

import (
	"context"
	"math/big"

	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

// SenderInfo is the on-chain TicketBroker view of a single sender.
// Returned by Broker.GetSenderInfo. Numerical fields are *big.Int so
// arithmetic in escrow / sender code can use exact math.
type SenderInfo struct {
	Deposit        *big.Int // wei
	Reserve        *Reserve
	WithdrawRound  int64    // 0 if no unlock pending
}

// Reserve breaks down the sender's reserve pool. `FundsRemaining` is
// what's still claimable; `Claimed[recipient]` records what each
// recipient has already pulled this round.
type Reserve struct {
	FundsRemaining *big.Int
	Claimed        map[string]*big.Int // hex-encoded recipient -> claimed wei
}

// Ticket is the in-process ticket shape the broker packs for on-chain
// submission. Mirrors internal/types.Ticket — duplicated here as a
// minimal struct so the providers package stays free of import cycles
// against internal/types (which itself imports proto-go bindings).
type Ticket struct {
	Recipient         []byte
	Sender            []byte
	FaceValue         *big.Int
	WinProb           *big.Int
	SenderNonce       uint32
	RecipientRandHash []byte
	CreationRound     int64
	CreationRoundHash []byte
}

// Broker is the on-chain TicketBroker provider. v0.2's dev fake returns
// canned values; plan 0016 implements this against the real
// TicketBroker contract on Arbitrum.
type Broker interface {
	// GetSenderInfo returns the current on-chain state for a sender.
	// The 20-byte sender bytes are matched against the contract's
	// senders mapping.
	GetSenderInfo(ctx context.Context, sender []byte) (*SenderInfo, error)

	// IsUsedTicket reports whether the on-chain TicketBroker has already
	// accepted this ticket-hash. Used as a redemption pre-check to
	// drain locally-queued duplicates without spending gas.
	IsUsedTicket(ctx context.Context, ticketHash []byte) (bool, error)

	// RedeemWinningTicket submits a winning-ticket redemption tx and
	// waits for it to confirm. The full Ticket is needed because the
	// contract recomputes the hash from the raw fields. Returns the
	// confirmed transaction hash on success; sentinel errors on
	// terminal failure (see settlement.IsNonRetryable).
	RedeemWinningTicket(ctx context.Context, ticket *Ticket, sig []byte, recipientRand *big.Int) (txHash []byte, err error)
}

// KeyStore signs ticket hashes. v0.2's dev fake uses a deterministic
// in-memory ECDSA key (or returns 65 zero bytes when configured to
// skip signatures); plan 0016 wires the V3 JSON keystore.
type KeyStore interface {
	// Address returns the ETH address this keystore signs as (20
	// bytes).
	Address() []byte

	// Sign returns a 65-byte ECDSA signature `[R || S || V]` over
	// `accounts.TextHash(hash)` — i.e., the input wrapped in EIP-191
	// `personal_sign`. V ∈ {27, 28}.
	Sign(hash []byte) ([]byte, error)
}

// TxSigner signs Ethereum transactions for on-chain submission. Receiver
// mode wires both KeyStore (EIP-191 ticket signing) and TxSigner
// (transaction signing for `redeemWinningTicket`); sender mode wires only
// KeyStore. Both interfaces consume the same loaded *ecdsa.PrivateKey.
type TxSigner interface {
	// SignTx signs `tx` for the given chain ID, returning the signed
	// transaction. Implementations must use EIP-155 (or later, e.g.
	// EIP-1559) signing — the legacy unprotected form is rejected.
	SignTx(tx *ethtypes.Transaction, chainID *big.Int) (*ethtypes.Transaction, error)
}

// Clock exposes Livepeer round and L1 block state. v0.2's dev fake
// returns deterministic canned values; plan 0016 polls
// RoundsManager.LastInitializedRound and BondingManager.
type Clock interface {
	// LastInitializedRound returns the most recent fully-initialized
	// Livepeer protocol round.
	LastInitializedRound() int64

	// LastInitializedL1BlockHash returns the L1 block hash associated
	// with LastInitializedRound.
	LastInitializedL1BlockHash() []byte

	// LastSeenL1Block returns the most recent L1 block number observed
	// by the daemon.
	LastSeenL1Block() *big.Int

	// GetTranscoderPoolSize returns the current size of the
	// BondingManager transcoder pool. Used by escrow.reserveAlloc to
	// divide a sender's total reserve across active recipients.
	GetTranscoderPoolSize() *big.Int
}

// GasPrice returns the current chain `eth_gasPrice` value. v0.2's dev
// fake returns a constant; plan 0016 polls the RPC endpoint on a
// configurable refresh interval and applies the operator-tuned
// multiplier.
type GasPrice interface {
	// Current returns the most-recent observed gas price in wei,
	// already multiplied by the operator-tuned multiplier (e.g.
	// `eth_gasPrice × 200%` on Arbitrum One).
	Current() *big.Int
}
