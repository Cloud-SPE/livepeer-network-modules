// Package minter is a placeholder ABI binding for Livepeer's Minter
// contract. Currently only exposes the contract address — the reward
// service decodes earned amounts from the BondingManager.Reward event,
// not from Minter calls.
//
// The package exists so address resolution + preflight have a uniform
// shape across the three on-chain contracts protocol-daemon touches
// (RoundsManager, BondingManager, Minter). When the daemon grows
// inflation-prediction or pre-mint-balance checks, they live here.
package minter

import (
	"errors"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/rpc"
)

// Bindings is the protocol-daemon-facing surface for Minter (read-only).
type Bindings struct {
	rpc  rpc.RPC
	addr chain.Address
}

// New constructs Bindings for the contract at addr.
func New(r rpc.RPC, addr chain.Address) (*Bindings, error) {
	if r == nil {
		return nil, errors.New("minter: rpc is required")
	}
	// Zero address is allowed for now — Minter resolution is best-effort
	// since we don't currently call it.
	return &Bindings{rpc: r, addr: addr}, nil
}

// Address returns the contract address.
func (b *Bindings) Address() chain.Address { return b.addr }
