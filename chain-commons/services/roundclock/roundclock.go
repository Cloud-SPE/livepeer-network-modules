// Package roundclock emits typed Round events on top of the timesource and
// logs providers, with last-emitted round persisted to the store so a
// daemon restart does not re-fire stale Round events.
//
// Implementation lands in a follow-up commit; this file pins the public
// interface so consumer daemons can compile against it.
package roundclock

import (
	"context"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
)

// Clock emits typed Round events.
type Clock interface {
	// SubscribeRounds returns a channel that receives a typed Round event
	// every time the active round changes. The first value sent is always
	// the current round (so consumers don't need to call Current first).
	SubscribeRounds(ctx context.Context) (<-chan chain.Round, error)

	// SubscribeL1Blocks returns a channel of L1 block-number transitions.
	SubscribeL1Blocks(ctx context.Context) (<-chan chain.BlockNumber, error)

	// Current returns the current Round without subscribing.
	Current(ctx context.Context) (chain.Round, error)
}
