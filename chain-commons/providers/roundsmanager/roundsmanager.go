// Package roundsmanager provides read-only ABI bindings to Livepeer's
// RoundsManager contract — the calls every chain-aware daemon needs:
//
//   - currentRoundInitialized() → bool
//   - currentRound() → uint256
//   - lastInitializedRound() → uint256
//   - blockHashForRound(uint256) → bytes32
//
// Write-side calldata (initializeRound) lives in protocol-daemon's
// roundsmanager package, since it is coupled to the round-init service.
//
// All eth_calls go through chain-commons.providers.rpc; this package is
// the boundary for go-ethereum imports for these contract reads.
package roundsmanager

import (
	"context"
	"errors"
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/rpc"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/crypto"
)

// Selectors — keccak256(signature)[:4]. Computed at init for stable values.
var (
	selectorCurrentRoundInitialized = crypto.Keccak256([]byte("currentRoundInitialized()"))[:4]
	selectorCurrentRound            = crypto.Keccak256([]byte("currentRound()"))[:4]
	selectorLastInitializedRound    = crypto.Keccak256([]byte("lastInitializedRound()"))[:4]
	selectorBlockHashForRound       = crypto.Keccak256([]byte("blockHashForRound(uint256)"))[:4]
)

// Bindings is the read-only surface for RoundsManager.
type Bindings struct {
	rpc  rpc.RPC
	addr chain.Address
}

// New constructs a Bindings. addr is RoundsManager's deployed address
// (resolved from chain-commons.providers.controller.Addresses().RoundsManager).
func New(r rpc.RPC, addr chain.Address) (*Bindings, error) {
	if r == nil {
		return nil, errors.New("roundsmanager: rpc is required")
	}
	if addr == (chain.Address{}) {
		return nil, errors.New("roundsmanager: addr is required")
	}
	return &Bindings{rpc: r, addr: addr}, nil
}

// Address returns the contract address.
func (b *Bindings) Address() chain.Address { return b.addr }

// CurrentRoundInitialized calls RoundsManager.currentRoundInitialized() and
// returns whether the current round has been initialized.
func (b *Bindings) CurrentRoundInitialized(ctx context.Context) (bool, error) {
	addr := b.addr
	out, err := b.rpc.CallContract(ctx, ethereum.CallMsg{
		To:   &addr,
		Data: selectorCurrentRoundInitialized,
	}, nil)
	if err != nil {
		return false, fmt.Errorf("roundsmanager.currentRoundInitialized: %w", err)
	}
	if len(out) < 32 {
		return false, fmt.Errorf("roundsmanager.currentRoundInitialized: short return (%d bytes)", len(out))
	}
	// bool is encoded as 32 bytes; non-zero last byte == true.
	for _, b := range out[:32] {
		if b != 0 {
			return true, nil
		}
	}
	return false, nil
}

// CurrentRound calls RoundsManager.currentRound() and returns the round
// number. Useful for status RPCs and sanity-checking round-sensitive
// services.
func (b *Bindings) CurrentRound(ctx context.Context) (chain.RoundNumber, error) {
	addr := b.addr
	out, err := b.rpc.CallContract(ctx, ethereum.CallMsg{
		To:   &addr,
		Data: selectorCurrentRound,
	}, nil)
	if err != nil {
		return 0, fmt.Errorf("roundsmanager.currentRound: %w", err)
	}
	if len(out) < 32 {
		return 0, fmt.Errorf("roundsmanager.currentRound: short return (%d bytes)", len(out))
	}
	return chain.RoundNumber(decodeUint64(out[:32])), nil
}

// LastInitializedRound calls RoundsManager.lastInitializedRound(): the
// most recent round whose initializeRound() has landed, and therefore
// the most recent round with a non-zero blockHashForRound. Ticket
// creation rounds are anchored to this, not to currentRound(), because
// a ticket's creationRoundBlockHash must be readable by the redeemer.
func (b *Bindings) LastInitializedRound(ctx context.Context) (chain.RoundNumber, error) {
	addr := b.addr
	out, err := b.rpc.CallContract(ctx, ethereum.CallMsg{
		To:   &addr,
		Data: selectorLastInitializedRound,
	}, nil)
	if err != nil {
		return 0, fmt.Errorf("roundsmanager.lastInitializedRound: %w", err)
	}
	if len(out) < 32 {
		return 0, fmt.Errorf("roundsmanager.lastInitializedRound: short return (%d bytes)", len(out))
	}
	return chain.RoundNumber(decodeUint64(out[:32])), nil
}

// BlockHashForRound calls RoundsManager.blockHashForRound(round): the L1
// block hash recorded when the round was initialized. Zero for a round
// that was never initialized.
func (b *Bindings) BlockHashForRound(ctx context.Context, round chain.RoundNumber) (chain.TxHash, error) {
	addr := b.addr
	data := make([]byte, 4+32)
	copy(data[:4], selectorBlockHashForRound)
	// uint256 argument: big-endian in the rightmost bytes of the slot.
	v := uint64(round)
	for i := 35; i >= 28; i-- {
		data[i] = byte(v)
		v >>= 8
	}
	out, err := b.rpc.CallContract(ctx, ethereum.CallMsg{
		To:   &addr,
		Data: data,
	}, nil)
	if err != nil {
		return chain.TxHash{}, fmt.Errorf("roundsmanager.blockHashForRound(%d): %w", round, err)
	}
	if len(out) < 32 {
		return chain.TxHash{}, fmt.Errorf("roundsmanager.blockHashForRound(%d): short return (%d bytes)", round, len(out))
	}
	var h chain.TxHash
	copy(h[:], out[:32])
	return h, nil
}

// decodeUint64 reads a uint256 ABI-encoded value's low 8 bytes as uint64.
// Used for round numbers and similar small uints. The ABI layout is
// big-endian in the rightmost bytes of the 32-byte slot.
func decodeUint64(in []byte) uint64 {
	if len(in) < 32 {
		return 0
	}
	var v uint64
	for i := 24; i < 32; i++ {
		v = (v << 8) | uint64(in[i])
	}
	return v
}
