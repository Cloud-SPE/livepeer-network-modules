package roundclock

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/timesource"
)

const lastEmittedBucket = "chain_commons_roundclock_last_emitted"

// Options wires a roundclock.Clock implementation. TimeSource is required.
//
// Store is optional: when set, SubscribeRoundsForName persists the last-
// emitted round number per subscriber name, so a daemon restart doesn't
// re-fire a Round event that the consumer has already processed. When
// Store is nil, SubscribeRoundsForName degrades to the same behaviour as
// SubscribeRounds (no dedup across restarts).
type Options struct {
	TimeSource timesource.TimeSource
	Store      store.Store
	Logger     logger.Logger
}

// New returns a roundclock.Clock backed by the configured TimeSource.
func New(opts Options) (Clock, error) {
	if opts.TimeSource == nil {
		return nil, errors.New("roundclock: TimeSource is required")
	}
	if opts.Store != nil {
		// Pre-create bucket so first SubscribeRoundsForName doesn't have to.
		if _, err := opts.Store.Bucket(lastEmittedBucket); err != nil {
			return nil, fmt.Errorf("roundclock: open last-emitted bucket: %w", err)
		}
	}
	return &clockImpl{ts: opts.TimeSource, store: opts.Store, logger: opts.Logger}, nil
}

// NamedClock extends Clock with a per-name subscription that persists the
// last-emitted round across daemon restarts. Implementations returned by
// New satisfy both Clock and NamedClock.
type NamedClock interface {
	Clock
	// SubscribeRoundsForName behaves like SubscribeRounds but persists the
	// last-emitted round under name. Restarted daemons re-subscribing under
	// the same name skip Round events whose Number is <= the persisted value.
	// The returned channel carries new (un-suppressed) Round events; consumers
	// must call AckRound(name, round) after they've durably acted on each.
	SubscribeRoundsForName(ctx context.Context, name string) (<-chan chain.Round, error)
	// AckRound persists name's last-emitted round. Subsequent restarts skip
	// Round events with Number <= round.
	AckRound(name string, round chain.RoundNumber) error
	// LastEmitted returns the persisted last-emitted round for name, or 0 if none.
	LastEmitted(name string) (chain.RoundNumber, error)
}

type clockImpl struct {
	ts     timesource.TimeSource
	store  store.Store
	logger logger.Logger
}

// Current implements Clock.
func (c *clockImpl) Current(ctx context.Context) (chain.Round, error) {
	return c.ts.CurrentRound(ctx)
}

// SubscribeRounds implements Clock — passthrough to timesource.
func (c *clockImpl) SubscribeRounds(ctx context.Context) (<-chan chain.Round, error) {
	return c.ts.SubscribeRounds(ctx)
}

// SubscribeL1Blocks implements Clock — passthrough to timesource.
func (c *clockImpl) SubscribeL1Blocks(ctx context.Context) (<-chan chain.BlockNumber, error) {
	return c.ts.SubscribeL1Blocks(ctx)
}

// SubscribeRoundsForName implements NamedClock with restart-safe dedup.
func (c *clockImpl) SubscribeRoundsForName(ctx context.Context, name string) (<-chan chain.Round, error) {
	if name == "" {
		return nil, errors.New("roundclock: name is required for named subscription")
	}
	upstream, err := c.ts.SubscribeRounds(ctx)
	if err != nil {
		return nil, err
	}

	last, err := c.LastEmitted(name)
	if err != nil {
		return nil, err
	}

	out := make(chan chain.Round, 4)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case r, ok := <-upstream:
				if !ok {
					return
				}
				if r.Number <= last {
					// Suppress: consumer's previous run already saw this round.
					if c.logger != nil {
						c.logger.Debug("roundclock.suppressed",
							logger.String("name", name),
							logger.Uint64("round", uint64(r.Number)),
							logger.Uint64("last_emitted", uint64(last)),
						)
					}
					continue
				}
				select {
				case out <- r:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// AckRound persists the round under name. No-op if Store wasn't configured.
func (c *clockImpl) AckRound(name string, round chain.RoundNumber) error {
	if c.store == nil {
		return errors.New("roundclock: Store not configured; cannot persist named state")
	}
	if name == "" {
		return errors.New("roundclock: name is required")
	}
	bucket, err := c.store.Bucket(lastEmittedBucket)
	if err != nil {
		return err
	}
	out := make([]byte, 8)
	binary.BigEndian.PutUint64(out, uint64(round))
	return bucket.Put([]byte(name), out)
}

// LastEmitted returns the persisted last-emitted round for name. Returns 0
// when no entry exists or when Store wasn't configured.
func (c *clockImpl) LastEmitted(name string) (chain.RoundNumber, error) {
	if c.store == nil || name == "" {
		return 0, nil
	}
	bucket, err := c.store.Bucket(lastEmittedBucket)
	if err != nil {
		return 0, err
	}
	v, err := bucket.Get([]byte(name))
	if err == store.ErrNotFound {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if len(v) != 8 {
		return 0, fmt.Errorf("roundclock: corrupt last-emitted entry for %q (len=%d)", name, len(v))
	}
	return chain.RoundNumber(binary.BigEndian.Uint64(v)), nil
}

// Compile-time: clockImpl satisfies both Clock and NamedClock.
var (
	_ Clock      = (*clockImpl)(nil)
	_ NamedClock = (*clockImpl)(nil)
)
