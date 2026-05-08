// Package logs provides the eth_getLogs poller with durable per-subscriber
// offsets.
//
// Each subscription registers under a stable name. The last block consumed
// (after the consumer Acks) is persisted in the Store, so daemon restart
// resumes from where it left off.
//
// See docs/design-docs/event-log-offsets.md for the full design.
package logs

import (
	"context"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
)

// Logs is the durable log subscriber.
type Logs interface {
	// Subscribe registers a logical subscription under name with the given
	// filter. Returns a Subscription that yields log batches via Events()
	// and accepts consumer commits via Ack(). Returns ErrSubscriptionExists
	// if a subscription already exists under name.
	Subscribe(ctx context.Context, name string, query ethereum.FilterQuery) (Subscription, error)

	// Unsubscribe closes the subscription registered under name. The
	// persisted offset is retained; a future Subscribe(name) resumes.
	Unsubscribe(name string) error

	// LastConsumed returns the persisted last-acked block for name, or 0 if
	// no offset has been recorded.
	LastConsumed(name string) (chain.BlockNumber, error)
}

// Subscription is the consumer-side handle to a registered subscription.
type Subscription interface {
	// Events returns the channel of log batches. Each batch corresponds to
	// one polling cycle's chunk; a single Subscribe()-driven backfill may
	// emit many batches in sequence.
	Events() <-chan []types.Log

	// Ack records that the consumer has durably processed all events up to
	// (and including) throughBlock. The persisted offset advances. Future
	// polls start from throughBlock+1.
	Ack(throughBlock chain.BlockNumber) error

	// Close releases resources. Does not delete the persisted offset.
	Close() error
}
