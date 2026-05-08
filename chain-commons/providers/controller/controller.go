// Package controller provides the on-chain Controller-resolved sub-contract
// address abstraction.
//
// At startup, the implementation queries Controller.getContract(name) for
// each known sub-contract and caches the addresses. It refreshes
// periodically and notifies subscribers on change.
//
// See docs/design-docs/controller-resolver.md for the full design.
package controller

import (
	"context"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
)

// Controller is the sub-contract address resolver.
type Controller interface {
	// Addresses returns the current snapshot. Lock-free read; the underlying
	// pointer is atomic-swapped on refresh.
	Addresses() Addresses

	// Refresh forces a re-resolution. Used at startup and in test fixtures.
	Refresh(ctx context.Context) error

	// Subscribe returns a channel that receives a new Addresses snapshot every
	// time a refresh changes any address. Closed channels indicate the
	// Controller has been shut down.
	Subscribe() <-chan Addresses
}

// Addresses is the snapshot of resolved sub-contract addresses.
type Addresses struct {
	RoundsManager   chain.Address
	BondingManager  chain.Address
	Minter          chain.Address
	TicketBroker    chain.Address
	ServiceRegistry chain.Address
	LivepeerToken   chain.Address
	ResolvedAt      time.Time
}

// Names lists the contract names this resolver queries from Controller.
// Exported so tests and FakeController can populate the same set.
var Names = []string{
	"RoundsManager",
	"BondingManager",
	"Minter",
	"TicketBroker",
	"ServiceRegistry",
	"LivepeerToken",
}
