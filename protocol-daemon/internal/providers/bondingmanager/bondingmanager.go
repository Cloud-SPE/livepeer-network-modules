// Package bondingmanager wraps chain-commons.providers.bondingmanager
// (which exposes the read-only BondingManager surface) and adds the
// reward-flow-specific calldata builder + event-log decoders that
// protocol-daemon uses.
//
// Read-side methods (GetTranscoder, IsActiveTranscoder, pool walk) are
// promoted from the embedded chain-commons binding. Reward-side
// (PackRewardWithHint, DecodeRewardEvent, FindRewardForTranscoder)
// lives here.
package bondingmanager

import (
	"math/big"

	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	ccbm "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/bondingmanager"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/rpc"
)

// Reward-side selector. Read-only selectors live in chain-commons.
var selectorRewardWithHint = crypto.Keccak256([]byte("rewardWithHint(address,address)"))[:4]

// EventReward is the topic0 for the Reward(address indexed transcoder,
// uint256 amount) event. Used by the receipt-log decoder.
var EventReward = crypto.Keccak256Hash([]byte("Reward(address,uint256)"))

// Bindings is the protocol-daemon-facing surface for BondingManager.
// It embeds the chain-commons read-only binding (so Address,
// GetTranscoder, IsActiveTranscoder, GetFirstTranscoderInPool,
// GetNextTranscoderInPool are promoted) and adds reward-flow methods.
type Bindings struct {
	*ccbm.Bindings
}

// New constructs Bindings for the contract at addr. Delegates input
// validation to chain-commons.
func New(r rpc.RPC, addr chain.Address) (*Bindings, error) {
	inner, err := ccbm.New(r, addr)
	if err != nil {
		return nil, err
	}
	return &Bindings{Bindings: inner}, nil
}

// PackRewardWithHint returns the calldata for BondingManager.rewardWithHint(prev, next).
func (b *Bindings) PackRewardWithHint(prev, next chain.Address) ([]byte, error) {
	out := make([]byte, 4+32+32)
	copy(out[0:4], selectorRewardWithHint)
	copy(out[4+12:4+32], prev[:])
	copy(out[4+32+12:4+64], next[:])
	return out, nil
}

// DecodeRewardEvent extracts (transcoder, amount) from a Reward event log.
// Returns ok=false if the log doesn't match the Reward signature.
//
// The Reward event signature is:
//
//	event Reward(address indexed transcoder, uint256 amount)
//
// topic[0] = keccak256("Reward(address,uint256)")
// topic[1] = transcoder address
// data     = amount (32 bytes uint256)
func DecodeRewardEvent(log ethtypes.Log) (transcoder chain.Address, amount *big.Int, ok bool) {
	if len(log.Topics) < 2 {
		return chain.Address{}, nil, false
	}
	if log.Topics[0] != EventReward {
		return chain.Address{}, nil, false
	}
	// topic[1] is the indexed address — rightmost 20 bytes.
	t := log.Topics[1]
	var addr chain.Address
	copy(addr[:], t[12:])
	if len(log.Data) < 32 {
		return chain.Address{}, nil, false
	}
	amt := new(big.Int).SetBytes(log.Data[:32])
	return addr, amt, true
}

// FindRewardForTranscoder searches a slice of logs for the first Reward
// event matching `addr`. Returns (amount, true) on hit; (zero, false)
// otherwise.
func FindRewardForTranscoder(logs []ethtypes.Log, addr chain.Address) (*big.Int, bool) {
	for _, l := range logs {
		t, amt, ok := DecodeRewardEvent(l)
		if !ok {
			continue
		}
		if t == addr {
			return amt, true
		}
	}
	return new(big.Int), false
}

// EncodeAddressSlot is re-exported from chain-commons for tests that
// synthesize CallContract responses.
var EncodeAddressSlot = ccbm.EncodeAddressSlot

// EncodeUintSlot is re-exported from chain-commons for tests.
var EncodeUintSlot = ccbm.EncodeUintSlot

// EncodeBoolSlot is re-exported from chain-commons for tests.
var EncodeBoolSlot = ccbm.EncodeBoolSlot

// TranscoderInfo is re-exported from chain-commons so consumers that
// reference the protocol-daemon binding's return type don't need to
// know about chain-commons' package layout.
type TranscoderInfo = ccbm.TranscoderInfo
