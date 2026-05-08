// Package poller provides a logs.Logs implementation backed by polled
// eth_getLogs calls with durable per-subscriber offsets persisted via the
// store.Store provider.
//
// Each subscriber registers under a stable name. The persisted offset is
// updated only when the consumer Acks. Daemon restart resumes from the
// persisted offset; a brand-new name without persistence starts from
// query.FromBlock or the current head, never from genesis.
package poller

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	cerrors "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/errors"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/clock"
	clogs "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logs"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/rpc"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/store"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
)

const offsetBucket = "chain_commons_log_offsets"

// ErrSubscriptionExists is returned by Subscribe when a subscription
// already exists under the given name.
var ErrSubscriptionExists = errors.New("logs: subscription already exists")

// Options wires the logs poller.
type Options struct {
	RPC                rpc.RPC
	Store              store.Store
	PollInterval       time.Duration
	ChunkSize          uint64
	ReorgConfirmations uint64
	Clock              clock.Clock
	Logger             logger.Logger
}

// New constructs a logs poller. RPC + Store are required.
func New(opts Options) (clogs.Logs, error) {
	if opts.RPC == nil {
		return nil, errors.New("logs-poller: RPC is required")
	}
	if opts.Store == nil {
		return nil, errors.New("logs-poller: Store is required")
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = 5 * time.Second
	}
	if opts.ChunkSize == 0 {
		opts.ChunkSize = 1000
	}
	if opts.ReorgConfirmations == 0 {
		opts.ReorgConfirmations = 4
	}
	if opts.Clock == nil {
		opts.Clock = clock.System()
	}
	if _, err := opts.Store.Bucket(offsetBucket); err != nil {
		return nil, fmt.Errorf("logs-poller: open offset bucket: %w", err)
	}
	return &logsPoller{
		rpc:                opts.RPC,
		store:              opts.Store,
		poll:               opts.PollInterval,
		chunk:              opts.ChunkSize,
		reorgConfirmations: opts.ReorgConfirmations,
		clock:              opts.Clock,
		logger:             opts.Logger,
		active:             make(map[string]*subscription),
	}, nil
}

type logsPoller struct {
	rpc                rpc.RPC
	store              store.Store
	poll               time.Duration
	chunk              uint64
	reorgConfirmations uint64
	clock              clock.Clock
	logger             logger.Logger

	mu     sync.Mutex
	active map[string]*subscription
}

func (p *logsPoller) Subscribe(ctx context.Context, name string, query ethereum.FilterQuery) (clogs.Subscription, error) {
	p.mu.Lock()
	if _, exists := p.active[name]; exists {
		p.mu.Unlock()
		return nil, ErrSubscriptionExists
	}

	last, err := p.LastConsumed(name)
	if err != nil {
		p.mu.Unlock()
		return nil, err
	}

	from := last + 1
	if last == 0 {
		// New name with no persisted offset.
		if query.FromBlock != nil {
			from = chain.BlockNumber(query.FromBlock.Uint64())
		} else {
			head, err := p.rpc.HeaderByNumber(ctx, nil)
			if err != nil {
				p.mu.Unlock()
				return nil, cerrors.Wrap(cerrors.ClassTransient, "logs.head_failed",
					"failed to fetch head for new subscription", err)
			}
			from = chain.BlockNumber(head.Number.Uint64())
		}
	}

	sub := &subscription{
		owner:  p,
		name:   name,
		query:  query,
		from:   from,
		events: make(chan []types.Log, 4),
		stop:   make(chan struct{}),
	}
	p.active[name] = sub
	p.mu.Unlock()

	sub.wg.Add(1)
	go sub.loop()
	return sub, nil
}

func (p *logsPoller) Unsubscribe(name string) error {
	p.mu.Lock()
	sub, ok := p.active[name]
	if !ok {
		p.mu.Unlock()
		return nil
	}
	delete(p.active, name)
	p.mu.Unlock()
	return sub.Close()
}

func (p *logsPoller) LastConsumed(name string) (chain.BlockNumber, error) {
	bucket, err := p.store.Bucket(offsetBucket)
	if err != nil {
		return 0, err
	}
	value, err := bucket.Get([]byte(name))
	if err == store.ErrNotFound {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if len(value) != 8 {
		return 0, fmt.Errorf("logs-poller: corrupt offset for %q (len=%d)", name, len(value))
	}
	return chain.BlockNumber(binary.BigEndian.Uint64(value)), nil
}

func (p *logsPoller) saveOffset(name string, block chain.BlockNumber) error {
	bucket, err := p.store.Bucket(offsetBucket)
	if err != nil {
		return err
	}
	out := make([]byte, 8)
	binary.BigEndian.PutUint64(out, uint64(block))
	return bucket.Put([]byte(name), out)
}

type subscription struct {
	owner  *logsPoller
	name   string
	query  ethereum.FilterQuery
	from   chain.BlockNumber
	events chan []types.Log
	stop   chan struct{}
	wg     sync.WaitGroup
}

func (s *subscription) Events() <-chan []types.Log { return s.events }

func (s *subscription) Ack(throughBlock chain.BlockNumber) error {
	return s.owner.saveOffset(s.name, throughBlock)
}

func (s *subscription) Close() error {
	close(s.stop)
	s.wg.Wait()
	close(s.events)
	return nil
}

func (s *subscription) loop() {
	defer s.wg.Done()
	t := s.owner.clock.NewTicker(s.owner.poll)
	defer t.Stop()

	for {
		select {
		case <-s.stop:
			return
		case <-t.C():
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			s.tickOnce(ctx)
			cancel()
		}
	}
}

func (s *subscription) tickOnce(ctx context.Context) {
	head, err := s.owner.rpc.HeaderByNumber(ctx, nil)
	if err != nil {
		if s.owner.logger != nil {
			s.owner.logger.Warn("logs.head_failed", logger.String("name", s.name), logger.Err(err))
		}
		return
	}
	currentHead := chain.BlockNumber(head.Number.Uint64())

	// Reorg detection: if persisted offset is past the current head, rewind.
	if s.from > currentHead+1 {
		newFrom := chain.BlockNumber(0)
		if currentHead > chain.BlockNumber(s.owner.reorgConfirmations) {
			newFrom = currentHead - chain.BlockNumber(s.owner.reorgConfirmations)
		}
		if s.owner.logger != nil {
			s.owner.logger.Warn("logs.reorg_rewind",
				logger.String("name", s.name),
				logger.Uint64("from", uint64(s.from)),
				logger.Uint64("to", uint64(newFrom)),
			)
		}
		_ = s.owner.saveOffset(s.name, newFrom)
		s.from = newFrom + 1
		return
	}

	if s.from > currentHead {
		return // nothing new
	}

	// Fetch in chunks.
	from := s.from
	for from <= currentHead {
		to := from + chain.BlockNumber(s.owner.chunk-1)
		if to > currentHead {
			to = currentHead
		}
		query := s.query
		query.FromBlock = new(big.Int).SetUint64(uint64(from))
		query.ToBlock = new(big.Int).SetUint64(uint64(to))

		logs, err := s.owner.rpc.FilterLogs(ctx, query)
		if err != nil {
			if s.owner.logger != nil {
				s.owner.logger.Warn("logs.filter_failed",
					logger.String("name", s.name),
					logger.Uint64("from", uint64(from)),
					logger.Uint64("to", uint64(to)),
					logger.Err(err),
				)
			}
			return
		}

		select {
		case s.events <- logs:
		case <-s.stop:
			return
		}

		from = to + 1
	}
	s.from = from
}

// Compile-time: logsPoller satisfies clogs.Logs.
var _ clogs.Logs = (*logsPoller)(nil)
