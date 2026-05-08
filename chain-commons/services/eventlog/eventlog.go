// Package eventlog wraps providers/logs with stronger lifecycle semantics
// for service-level consumers.
//
// One subscriber per logical use case (not one per goroutine). Implementation
// lands in a follow-up commit; this file pins the public interface.
package eventlog

import (
	"context"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
)

// EventLog is the durable per-subscriber log subscription.
type EventLog interface {
	// Subscribe registers a logical subscription under name. Backfills from
	// the persisted offset (or query.FromBlock if unset and no persisted
	// offset exists). Returns ErrSubscriptionExists for duplicate names.
	Subscribe(ctx context.Context, name string, query ethereum.FilterQuery) (Subscription, error)
}

// Subscription is the consumer handle.
type Subscription interface {
	Events() <-chan []types.Log
	Ack(throughBlock chain.BlockNumber) error
	Close() error
}
