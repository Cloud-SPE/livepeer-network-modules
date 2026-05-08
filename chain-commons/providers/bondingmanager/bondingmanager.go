// Package bondingmanager provides read-only ABI bindings to Livepeer's
// BondingManager contract — the calls every chain-aware daemon needs:
//
//   - getTranscoder(address) → struct
//   - isActiveTranscoder(address) → bool
//   - getFirstTranscoderInPool() → address
//   - getNextTranscoderInPool(address) → address
//
// Reward-flow-specific calldata builders (rewardWithHint) and event log
// decoders (Reward) live in protocol-daemon's bondingmanager package,
// since they are write-side concerns coupled to the reward service.
//
// All eth_calls go through chain-commons.providers.rpc; this package is
// the boundary for go-ethereum imports for these contract reads.
package bondingmanager

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/rpc"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/crypto"
)

// Selectors — keccak256(signature)[:4]. Computed at init for stable values.
//
// getTranscoder's return tuple varies by Livepeer protocol revision. We
// surface the fields callers use (lastRewardRound, activationRound,
// deactivationRound) and ignore others; the ABI decoder in this package
// only reads the slots needed by index from the raw return bytes.
var (
	selectorGetTranscoder      = crypto.Keccak256([]byte("getTranscoder(address)"))[:4]
	selectorIsActiveTranscoder = crypto.Keccak256([]byte("isActiveTranscoder(address)"))[:4]
	selectorGetFirstInPool     = crypto.Keccak256([]byte("getFirstTranscoderInPool()"))[:4]
	selectorGetNextInPool      = crypto.Keccak256([]byte("getNextTranscoderInPool(address)"))[:4]
)

// Slot indices in getTranscoder's return tuple. The exact tuple shape
// (number and order of fields) is dictated by Livepeer protocol revision;
// this package mirrors the protocol's current shape:
//
//	slot 0: lastRewardRound
//	slot 1: rewardCut
//	slot 2: feeShare
//	slot 3: lastActiveStakeUpdateRound
//	slot 4: activationRound
//	slot 5: deactivationRound
//	slot 6: activeCumulativeRewards
//	slot 7: cumulativeRewards
//	slot 8: cumulativeFees
//	slot 9: lastFeeRound
const (
	slotLastRewardRound   = 0
	slotActivationRound   = 4
	slotDeactivationRound = 5
)

// TranscoderInfo is the subset of BondingManager.getTranscoder() the
// shared bindings expose. Other fields exist on chain (delegatedAmount,
// rewardCut, etc.) and can be added when a consumer needs them.
type TranscoderInfo struct {
	Address           chain.Address
	Active            bool
	LastRewardRound   chain.RoundNumber
	ActivationRound   chain.RoundNumber
	DeactivationRound chain.RoundNumber
}

// IsActiveAtRound reports whether the transcoder is active at the given
// round. Mirrors BondingManager.isActiveTranscoder semantics:
// Active && ActivationRound <= round < DeactivationRound.
func (t TranscoderInfo) IsActiveAtRound(round chain.RoundNumber) bool {
	if !t.Active {
		return false
	}
	if t.ActivationRound > round {
		return false
	}
	if t.DeactivationRound != 0 && round >= t.DeactivationRound {
		return false
	}
	return true
}

// Bindings is the read-only surface for BondingManager.
type Bindings struct {
	rpc  rpc.RPC
	addr chain.Address
}

// New constructs Bindings for the contract at addr.
func New(r rpc.RPC, addr chain.Address) (*Bindings, error) {
	if r == nil {
		return nil, errors.New("bondingmanager: rpc is required")
	}
	if addr == (chain.Address{}) {
		return nil, errors.New("bondingmanager: addr is required")
	}
	return &Bindings{rpc: r, addr: addr}, nil
}

// Address returns the contract address.
func (b *Bindings) Address() chain.Address { return b.addr }

// GetTranscoder calls BondingManager.getTranscoder(addr) and decodes the
// fields the shared bindings expose (lastRewardRound, activationRound,
// deactivationRound). Combined with IsActiveTranscoder this gives the
// eligibility decision a reward service makes per round.
func (b *Bindings) GetTranscoder(ctx context.Context, addr chain.Address) (TranscoderInfo, error) {
	out, err := b.callWithAddress(ctx, selectorGetTranscoder, addr)
	if err != nil {
		return TranscoderInfo{}, fmt.Errorf("bondingmanager.getTranscoder: %w", err)
	}
	if len(out) < 32*6 {
		return TranscoderInfo{}, fmt.Errorf("bondingmanager.getTranscoder: short return (%d bytes)", len(out))
	}

	info := TranscoderInfo{Address: addr}
	info.LastRewardRound = chain.RoundNumber(decodeUint64(out, slotLastRewardRound))
	info.ActivationRound = chain.RoundNumber(decodeUint64(out, slotActivationRound))
	info.DeactivationRound = chain.RoundNumber(decodeUint64(out, slotDeactivationRound))

	// Active is determined separately via isActiveTranscoder; do that call
	// and stamp the field.
	active, err := b.IsActiveTranscoder(ctx, addr)
	if err != nil {
		return TranscoderInfo{}, err
	}
	info.Active = active

	return info, nil
}

// IsActiveTranscoder calls BondingManager.isActiveTranscoder(addr).
func (b *Bindings) IsActiveTranscoder(ctx context.Context, addr chain.Address) (bool, error) {
	out, err := b.callWithAddress(ctx, selectorIsActiveTranscoder, addr)
	if err != nil {
		return false, fmt.Errorf("bondingmanager.isActiveTranscoder: %w", err)
	}
	if len(out) < 32 {
		return false, fmt.Errorf("bondingmanager.isActiveTranscoder: short return (%d bytes)", len(out))
	}
	for _, v := range out[:32] {
		if v != 0 {
			return true, nil
		}
	}
	return false, nil
}

// GetFirstTranscoderInPool returns the first address in the active set's
// linked list. Returns the zero address when the pool is empty.
func (b *Bindings) GetFirstTranscoderInPool(ctx context.Context) (chain.Address, error) {
	addr := b.addr
	out, err := b.rpc.CallContract(ctx, ethereum.CallMsg{
		To:   &addr,
		Data: selectorGetFirstInPool,
	}, nil)
	if err != nil {
		return chain.Address{}, fmt.Errorf("bondingmanager.getFirstTranscoderInPool: %w", err)
	}
	if len(out) < 32 {
		return chain.Address{}, fmt.Errorf("bondingmanager.getFirstTranscoderInPool: short return")
	}
	return decodeAddress(out, 0), nil
}

// GetNextTranscoderInPool returns the address that comes after `addr` in
// the active set's linked list. Returns the zero address at the tail.
func (b *Bindings) GetNextTranscoderInPool(ctx context.Context, addr chain.Address) (chain.Address, error) {
	out, err := b.callWithAddress(ctx, selectorGetNextInPool, addr)
	if err != nil {
		return chain.Address{}, fmt.Errorf("bondingmanager.getNextTranscoderInPool: %w", err)
	}
	if len(out) < 32 {
		return chain.Address{}, fmt.Errorf("bondingmanager.getNextTranscoderInPool: short return")
	}
	return decodeAddress(out, 0), nil
}

// callWithAddress invokes a single-address-arg method (selector || addr)
// on the BondingManager contract.
func (b *Bindings) callWithAddress(ctx context.Context, selector []byte, arg chain.Address) ([]byte, error) {
	calldata := make([]byte, 4+32)
	copy(calldata[0:4], selector)
	copy(calldata[4+12:4+32], arg[:])
	addr := b.addr
	return b.rpc.CallContract(ctx, ethereum.CallMsg{
		To:   &addr,
		Data: calldata,
	}, nil)
}

// decodeUint64 reads slot N (32-byte ABI slot) of the return data as
// uint64 (low 8 bytes of the slot).
func decodeUint64(in []byte, slot int) uint64 {
	off := slot * 32
	if off+32 > len(in) {
		return 0
	}
	var v uint64
	for i := off + 24; i < off+32; i++ {
		v = (v << 8) | uint64(in[i])
	}
	return v
}

// decodeAddress reads slot N as an address (rightmost 20 bytes of the slot).
func decodeAddress(in []byte, slot int) chain.Address {
	off := slot * 32
	if off+32 > len(in) {
		return chain.Address{}
	}
	var a chain.Address
	copy(a[:], in[off+12:off+32])
	return a
}

// EncodeAddressSlot returns a 32-byte ABI-encoded address. Exposed for
// tests that synthesize CallContract responses.
func EncodeAddressSlot(a chain.Address) []byte {
	out := make([]byte, 32)
	copy(out[12:], a[:])
	return out
}

// EncodeUintSlot returns a 32-byte ABI-encoded uint256. Exposed for tests.
func EncodeUintSlot(v uint64) []byte {
	out := make([]byte, 32)
	new(big.Int).SetUint64(v).FillBytes(out)
	return out
}

// EncodeBoolSlot returns a 32-byte ABI-encoded bool. Exposed for tests.
func EncodeBoolSlot(v bool) []byte {
	out := make([]byte, 32)
	if v {
		out[31] = 1
	}
	return out
}
