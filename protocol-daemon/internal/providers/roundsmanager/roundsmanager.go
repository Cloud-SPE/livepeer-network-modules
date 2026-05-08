// Package roundsmanager wraps chain-commons.providers.roundsmanager
// (which exposes the read-only RoundsManager surface) and adds the
// initializeRound calldata builder protocol-daemon uses for its
// round-init service.
//
// Read-side methods (CurrentRound, CurrentRoundInitialized) are
// promoted from the embedded chain-commons binding. Write-side
// (PackInitializeRound) lives here.
package roundsmanager

import (
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	ccrm "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/roundsmanager"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/rpc"
)

// Write-side selector. Read-only selectors live in chain-commons.
var selectorInitializeRound = crypto.Keccak256([]byte("initializeRound()"))[:4]

// Bindings is the protocol-daemon-facing surface for RoundsManager.
// It embeds the chain-commons read-only binding (so Address,
// CurrentRound, CurrentRoundInitialized are promoted) and adds the
// write-side calldata packer.
type Bindings struct {
	*ccrm.Bindings
}

// New constructs a Bindings. Delegates input validation to chain-commons.
func New(r rpc.RPC, addr chain.Address) (*Bindings, error) {
	inner, err := ccrm.New(r, addr)
	if err != nil {
		return nil, err
	}
	return &Bindings{Bindings: inner}, nil
}

// PackInitializeRound returns the calldata for RoundsManager.initializeRound().
// No arguments; just the 4-byte selector.
func (b *Bindings) PackInitializeRound() ([]byte, error) {
	out := make([]byte, 4)
	copy(out, selectorInitializeRound)
	return out, nil
}
