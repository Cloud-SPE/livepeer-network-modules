package eventlog

import (
	"context"
	"errors"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	clogs "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logs"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
)

// Options wires the EventLog service over a providers/logs.Logs.
type Options struct {
	Logs clogs.Logs
}

// New returns an EventLog backed by the provided providers/logs.Logs.
//
// The current implementation is a thin pass-through: providers/logs already
// owns durable per-name offsets, restart resume, ack-driven advancement,
// and reorg rewind — service/eventlog exists primarily as a typed seam for
// downstream daemons that want to swap implementations or add lifecycle
// concerns later (cross-subscription orchestration, batch coalescing, etc).
func New(opts Options) (EventLog, error) {
	if opts.Logs == nil {
		return nil, errors.New("eventlog: Logs provider is required")
	}
	return &eventLog{logs: opts.Logs}, nil
}

type eventLog struct {
	logs clogs.Logs
}

// Subscribe implements EventLog.
func (e *eventLog) Subscribe(ctx context.Context, name string, query ethereum.FilterQuery) (Subscription, error) {
	if name == "" {
		return nil, errors.New("eventlog: name is required")
	}
	sub, err := e.logs.Subscribe(ctx, name, query)
	if err != nil {
		return nil, err
	}
	return &subWrapper{inner: sub}, nil
}

type subWrapper struct {
	inner clogs.Subscription
}

func (s *subWrapper) Events() <-chan []types.Log              { return s.inner.Events() }
func (s *subWrapper) Ack(throughBlock chain.BlockNumber) error { return s.inner.Ack(throughBlock) }
func (s *subWrapper) Close() error                              { return s.inner.Close() }

// Compile-time: types satisfy interfaces.
var (
	_ EventLog     = (*eventLog)(nil)
	_ Subscription = (*subWrapper)(nil)
)
