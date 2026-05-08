// Package roundsmanager provides read-only ABI bindings to Livepeer's
// RoundsManager contract — the calls every chain-aware daemon needs:
//
//   - currentRoundInitialized() → bool
//   - currentRound() → uint256
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

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/rpc"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/crypto"
)

// Selectors — keccak256(signature)[:4]. Computed at init for stable values.
var (
	selectorCurrentRoundInitialized = crypto.Keccak256([]byte("currentRoundInitialized()"))[:4]
	selectorCurrentRound            = crypto.Keccak256([]byte("currentRound()"))[:4]
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
