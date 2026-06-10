package bondingmanager

import (
	"context"
	"fmt"
	"math/big"
	"sort"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
)

// Read selectors for the active-set (SortedDoublyLL) queries used to
// compute transcoder position hints.
var (
	selectorTranscoderTotalStake = crypto.Keccak256([]byte("transcoderTotalStake(address)"))[:4]
	selectorPoolMaxSize          = crypto.Keccak256([]byte("getTranscoderPoolMaxSize()"))[:4]
)

// PoolEntry is a transcoder and its total stake — the key the active set
// (a SortedDoublyLL) is ordered by, descending.
type PoolEntry struct {
	Address chain.Address
	Stake   *big.Int
}

// TranscoderPoolHints are the prev/next neighbor hints for a single
// SortedDoublyLL position update. The zero address means "no neighbor"
// (head or tail of the list), which is exactly what the contract expects.
type TranscoderPoolHints struct {
	PosPrev chain.Address
	PosNext chain.Address
}

// TranscoderTotalStake calls BondingManager.transcoderTotalStake(addr) —
// the transcoder's total stake (self-bond + delegated), i.e. the key it is
// sorted by in the active set.
func (b *Bindings) TranscoderTotalStake(ctx context.Context, addr chain.Address) (*big.Int, error) {
	calldata := make([]byte, 4+32)
	copy(calldata[0:4], selectorTranscoderTotalStake)
	copy(calldata[4+12:4+32], addr[:])
	to := b.addr
	out, err := b.rpc.CallContract(ctx, ethereum.CallMsg{To: &to, Data: calldata}, nil)
	if err != nil {
		return nil, fmt.Errorf("bondingmanager.transcoderTotalStake: %w", err)
	}
	if len(out) < 32 {
		return nil, fmt.Errorf("bondingmanager.transcoderTotalStake: short return (%d bytes)", len(out))
	}
	return new(big.Int).SetBytes(out[:32]), nil
}

// GetTranscoderPoolMaxSize calls BondingManager.getTranscoderPoolMaxSize() —
// the maximum size of the active set.
func (b *Bindings) GetTranscoderPoolMaxSize(ctx context.Context) (*big.Int, error) {
	to := b.addr
	out, err := b.rpc.CallContract(ctx, ethereum.CallMsg{To: &to, Data: selectorPoolMaxSize}, nil)
	if err != nil {
		return nil, fmt.Errorf("bondingmanager.getTranscoderPoolMaxSize: %w", err)
	}
	if len(out) < 32 {
		return nil, fmt.Errorf("bondingmanager.getTranscoderPoolMaxSize: short return")
	}
	return new(big.Int).SetBytes(out[:32]), nil
}

// TranscoderPool walks the active-set linked list
// (getFirstTranscoderInPool + getNextTranscoderInPool), reading each
// transcoder's total stake, and returns the pool in descending-stake order
// (the list's native order). The walk is bounded by the pool's max size to
// guard against a malformed/cyclic list.
func (b *Bindings) TranscoderPool(ctx context.Context) ([]PoolEntry, error) {
	maxSize, err := b.GetTranscoderPoolMaxSize(ctx)
	if err != nil {
		return nil, err
	}
	// The list can never exceed maxSize; +1 slack so an off-by-one never
	// masks a genuine cycle as a clean truncation.
	limit := int(maxSize.Int64()) + 1

	cur, err := b.GetFirstTranscoderInPool(ctx)
	if err != nil {
		return nil, err
	}
	pool := make([]PoolEntry, 0, limit)
	seen := make(map[chain.Address]struct{}, limit)
	for cur != (chain.Address{}) {
		if _, dup := seen[cur]; dup {
			return nil, fmt.Errorf("bondingmanager.TranscoderPool: cycle detected at %s", cur.Hex())
		}
		if len(pool) >= limit {
			return nil, fmt.Errorf("bondingmanager.TranscoderPool: walk exceeded max size %d", limit)
		}
		seen[cur] = struct{}{}

		stake, err := b.TranscoderTotalStake(ctx, cur)
		if err != nil {
			return nil, err
		}
		pool = append(pool, PoolEntry{Address: cur, Stake: stake})

		cur, err = b.GetNextTranscoderInPool(ctx, cur)
		if err != nil {
			return nil, err
		}
	}
	return pool, nil
}

// TransferBondHints computes the four position hints transferBond needs when
// `orch` transfers part of its own self-bond to a receiver delegated to it.
//
// On chain, transferBond is an unbond followed by a rebond, both acting on
// the orchestrator's own self-delegation, so both the "old delegate" and
// "new delegate" repositions are the orchestrator itself:
//
//   - oldDelegate hints: orch repositioned at (currentStake - amount), the
//     transient key after unbondWithHint's decreaseTotalStake.
//   - newDelegate hints: orch repositioned back at currentStake, the key
//     after processRebond's increaseTotalStake (net change is zero).
//
// Supplying real hints turns each on-chain SortedDoublyLL update from an
// O(n) scan of the full active set into an O(1) validated insert — which is
// what keeps the transfer within a bounded gas limit. Mirrors go-livepeer
// eth/client.go's simulateTranscoderPoolUpdate.
//
// If the orchestrator is not in the active set, the contract does not
// reposition it, so zero hints are returned (and are correct).
func (b *Bindings) TransferBondHints(ctx context.Context, orch chain.Address, amount *big.Int) (oldHints, newHints TranscoderPoolHints, err error) {
	pool, err := b.TranscoderPool(ctx)
	if err != nil {
		return TranscoderPoolHints{}, TranscoderPoolHints{}, err
	}
	maxSize, err := b.GetTranscoderPoolMaxSize(ctx)
	if err != nil {
		return TranscoderPoolHints{}, TranscoderPoolHints{}, err
	}
	isFull := int64(len(pool)) >= maxSize.Int64()

	var current *big.Int
	for _, e := range pool {
		if e.Address == orch {
			current = e.Stake
			break
		}
	}
	if current == nil {
		return TranscoderPoolHints{}, TranscoderPoolHints{}, nil
	}

	oldStake := new(big.Int).Sub(current, amount)
	oldHints = simulatePoolUpdate(orch, oldStake, pool, isFull)
	newHints = simulatePoolUpdate(orch, current, pool, isFull)
	return oldHints, newHints, nil
}

// simulatePoolUpdate mirrors the on-chain SortedDoublyLL reposition of `del`
// to `newStake`: drop it, reinsert at the new key, re-sort by descending
// stake, then read the neighbors. A non-member joining a full pool evicts
// the lowest-ranked transcoder; repositioning an existing member does not
// change the pool size, so no eviction is modeled (this is where we diverge
// from go-livepeer's unconditional drop-last, which would wrongly remove a
// still-present transcoder when `del` sits near the tail).
func simulatePoolUpdate(del chain.Address, newStake *big.Int, pool []PoolEntry, isFull bool) TranscoderPoolHints {
	wasMember := false
	next := make([]PoolEntry, 0, len(pool)+1)
	for _, e := range pool {
		if e.Address == del {
			wasMember = true
			continue
		}
		next = append(next, e)
	}
	next = append(next, PoolEntry{Address: del, Stake: new(big.Int).Set(newStake)})

	sort.SliceStable(next, func(i, j int) bool {
		return next[i].Stake.Cmp(next[j].Stake) > 0
	})

	if isFull && !wasMember && len(next) > 0 {
		next = next[:len(next)-1]
	}
	return findHints(del, next)
}

// findHints returns del's prev/next neighbors in an already-sorted pool.
// Returns zero hints when del is absent or the only entry.
func findHints(del chain.Address, pool []PoolEntry) TranscoderPoolHints {
	var h TranscoderPoolHints
	if len(pool) <= 1 {
		return h
	}
	for i, e := range pool {
		if e.Address != del {
			continue
		}
		if i > 0 {
			h.PosPrev = pool[i-1].Address
		}
		if i < len(pool)-1 {
			h.PosNext = pool[i+1].Address
		}
		break
	}
	return h
}
