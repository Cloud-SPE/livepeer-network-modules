package bondingmanager

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
)

// PackTranscoder returns the calldata for
// BondingManager.transcoder(uint256 _rewardCut, uint256 _feeShare).
// Both values are in parts-per-million (1_000_000 == 100%).
func (b *Bindings) PackTranscoder(rewardCutPPM, feeSharePPM uint64) ([]byte, error) {
	out := make([]byte, 4+32*2)
	copy(out[0:4], selectorTranscoder)
	new(big.Int).SetUint64(rewardCutPPM).FillBytes(out[4 : 4+32])
	new(big.Int).SetUint64(feeSharePPM).FillBytes(out[4+32 : 4+64])
	return out, nil
}

// PackTransferBond returns the calldata for
// BondingManager.transferBond(address _delegator, uint256 _amount,
// address _oldDelegateNewPosPrev, address _oldDelegateNewPosNext,
// address _newDelegateNewPosPrev, address _newDelegateNewPosNext).
//
// The four hint addresses may be the zero address, in which case the
// contract performs the O(n) pool insertion walk itself.
func (b *Bindings) PackTransferBond(recipient chain.Address, amount *big.Int, oldPrev, oldNext, newPrev, newNext chain.Address) ([]byte, error) {
	if amount == nil || amount.Sign() <= 0 {
		return nil, fmt.Errorf("transferBond: amount must be > 0")
	}
	out := make([]byte, 4+32*6)
	copy(out[0:4], selectorTransferBond)
	putAddress(out, 0, recipient)
	putUint(out, 1, amount)
	putAddress(out, 2, oldPrev)
	putAddress(out, 3, oldNext)
	putAddress(out, 4, newPrev)
	putAddress(out, 5, newNext)
	return out, nil
}

// PackWithdrawFees returns the calldata for
// BondingManager.withdrawFees(address payable _recipient, uint256 _amount).
func (b *Bindings) PackWithdrawFees(recipient chain.Address, amount *big.Int) ([]byte, error) {
	if amount == nil || amount.Sign() <= 0 {
		return nil, fmt.Errorf("withdrawFees: amount must be > 0")
	}
	out := make([]byte, 4+32*2)
	copy(out[0:4], selectorWithdrawFees)
	putAddress(out, 0, recipient)
	putUint(out, 1, amount)
	return out, nil
}

// PendingStake calls BondingManager.pendingStake(addr, endRound) — the
// delegator's stake including unclaimed rewards through endRound. This is
// the basis for the transfer-bond excess calculation.
func (b *Bindings) PendingStake(ctx context.Context, addr chain.Address, endRound chain.RoundNumber) (*big.Int, error) {
	return b.callUintWithAddressRound(ctx, "pendingStake", selectorPendingStake, addr, endRound)
}

// PendingFees calls BondingManager.pendingFees(addr, endRound) — the
// delegator's claimable ETH fees through endRound.
func (b *Bindings) PendingFees(ctx context.Context, addr chain.Address, endRound chain.RoundNumber) (*big.Int, error) {
	return b.callUintWithAddressRound(ctx, "pendingFees", selectorPendingFees, addr, endRound)
}

// DelegatorInfo is the subset of getDelegator the bonding-admin flows use.
type DelegatorInfo struct {
	BondedAmount    *big.Int
	Fees            *big.Int
	DelegateAddress chain.Address
}

// GetDelegator calls BondingManager.getDelegator(addr) and decodes the
// fields the admin flows / console display need.
func (b *Bindings) GetDelegator(ctx context.Context, addr chain.Address) (DelegatorInfo, error) {
	calldata := make([]byte, 4+32)
	copy(calldata[0:4], selectorGetDelegator)
	copy(calldata[4+12:4+32], addr[:])
	to := b.addr
	out, err := b.rpc.CallContract(ctx, ethereum.CallMsg{To: &to, Data: calldata}, nil)
	if err != nil {
		return DelegatorInfo{}, fmt.Errorf("bondingmanager.getDelegator: %w", err)
	}
	// getDelegator returns (bondedAmount, fees, delegateAddress,
	// delegatedAmount, startRound, lastClaimRound, nextUnbondingLockId).
	if len(out) < 32*3 {
		return DelegatorInfo{}, fmt.Errorf("bondingmanager.getDelegator: short return (%d bytes)", len(out))
	}
	return DelegatorInfo{
		BondedAmount:    decodeUint(out, 0),
		Fees:            decodeUint(out, 1),
		DelegateAddress: decodeAddr(out, 2),
	}, nil
}

// callUintWithAddressRound invokes a (address, uint256)->uint256 read.
func (b *Bindings) callUintWithAddressRound(ctx context.Context, name string, selector []byte, addr chain.Address, round chain.RoundNumber) (*big.Int, error) {
	calldata := make([]byte, 4+32*2)
	copy(calldata[0:4], selector)
	copy(calldata[4+12:4+32], addr[:])
	new(big.Int).SetUint64(uint64(round)).FillBytes(calldata[4+32 : 4+64])
	to := b.addr
	out, err := b.rpc.CallContract(ctx, ethereum.CallMsg{To: &to, Data: calldata}, nil)
	if err != nil {
		return nil, fmt.Errorf("bondingmanager.%s: %w", name, err)
	}
	if len(out) < 32 {
		return nil, fmt.Errorf("bondingmanager.%s: short return (%d bytes)", name, len(out))
	}
	return new(big.Int).SetBytes(out[:32]), nil
}

// putAddress writes a left-padded address into ABI word `slot` of the
// args region (after the 4-byte selector).
func putAddress(out []byte, slot int, a chain.Address) {
	off := 4 + slot*32
	copy(out[off+12:off+32], a[:])
}

// putUint writes a uint256 into ABI word `slot` of the args region.
func putUint(out []byte, slot int, v *big.Int) {
	off := 4 + slot*32
	v.FillBytes(out[off : off+32])
}

// decodeUint reads ABI word `slot` of return data as a uint256.
func decodeUint(in []byte, slot int) *big.Int {
	off := slot * 32
	if off+32 > len(in) {
		return new(big.Int)
	}
	return new(big.Int).SetBytes(in[off : off+32])
}

// decodeAddr reads ABI word `slot` of return data as an address.
func decodeAddr(in []byte, slot int) chain.Address {
	off := slot * 32
	var a chain.Address
	if off+32 > len(in) {
		return a
	}
	copy(a[:], in[off+12:off+32])
	return a
}
