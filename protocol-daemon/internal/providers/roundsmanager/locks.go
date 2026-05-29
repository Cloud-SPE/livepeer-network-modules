package roundsmanager

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
)

// Lock-window read selectors. These reads let the daemon compute the
// deterministic lock block for a round
// (startBlock + roundLength − roundLockAmount) so it can fire round-locked
// actions off the L1 block stream instead of polling currentRoundLocked.
var (
	selectorCurrentRoundLocked     = crypto.Keccak256([]byte("currentRoundLocked()"))[:4]
	selectorCurrentRoundStartBlock = crypto.Keccak256([]byte("currentRoundStartBlock()"))[:4]
	selectorRoundLength            = crypto.Keccak256([]byte("roundLength()"))[:4]
	selectorRoundLockAmount        = crypto.Keccak256([]byte("roundLockAmount()"))[:4]
)

// CurrentRoundLocked reports whether the current round is within its lock
// window (block >= startBlock + roundLength − roundLockAmount). This is the
// authoritative gate read used immediately before a fund-movement submit.
func (b *Bindings) CurrentRoundLocked(ctx context.Context) (bool, error) {
	out, err := b.callNoArg(ctx, "currentRoundLocked", selectorCurrentRoundLocked)
	if err != nil {
		return false, err
	}
	if len(out) < 32 {
		return false, fmt.Errorf("roundsmanager.currentRoundLocked: short return")
	}
	return out[len(out)-1] != 0, nil
}

// CurrentRoundStartBlock returns the L1 block at which the current round
// started.
func (b *Bindings) CurrentRoundStartBlock(ctx context.Context) (chain.BlockNumber, error) {
	return b.callUintNoArg(ctx, "currentRoundStartBlock", selectorCurrentRoundStartBlock)
}

// RoundLength returns the round length in L1 blocks.
func (b *Bindings) RoundLength(ctx context.Context) (chain.BlockNumber, error) {
	return b.callUintNoArg(ctx, "roundLength", selectorRoundLength)
}

// RoundLockAmount returns the lock window size in L1 blocks measured back
// from the end of the round.
func (b *Bindings) RoundLockAmount(ctx context.Context) (chain.BlockNumber, error) {
	return b.callUintNoArg(ctx, "roundLockAmount", selectorRoundLockAmount)
}

func (b *Bindings) callUintNoArg(ctx context.Context, name string, selector []byte) (chain.BlockNumber, error) {
	out, err := b.callNoArg(ctx, name, selector)
	if err != nil {
		return 0, err
	}
	if len(out) < 32 {
		return 0, fmt.Errorf("roundsmanager.%s: short return", name)
	}
	return chain.BlockNumber(new(big.Int).SetBytes(out[:32]).Uint64()), nil
}

func (b *Bindings) callNoArg(ctx context.Context, name string, selector []byte) ([]byte, error) {
	to := b.addr
	out, err := b.rpc.CallContract(ctx, ethereum.CallMsg{To: &to, Data: selector}, nil)
	if err != nil {
		return nil, fmt.Errorf("roundsmanager.%s: %w", name, err)
	}
	return out, nil
}
