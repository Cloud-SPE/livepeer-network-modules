// Package lifecycle coordinates the daemon's start/stop sequence.
//
// Run does the preflight, kicks off the round-init service goroutine
// (when in mode), kicks off the reward service goroutine (when in mode),
// and blocks until ctx is cancelled. Goroutines wait on a shared
// errgroup-style waitgroup; first error propagates up.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/roundclock"
	grpcrt "github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/runtime/grpc"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/reward"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/roundinit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/types"
)

// Config wires the lifecycle.
type Config struct {
	Mode       types.Mode
	RoundInit  *roundinit.Service // nil when Mode=reward
	Reward     *reward.Service    // nil when Mode=round-init
	RoundClock roundclock.Clock
	Listener   *grpcrt.Listener // nil → no gRPC listener (used by tests / dry-runs)
	Logger     logger.Logger
}

// Run kicks off the configured services and blocks until ctx is cancelled
// or one of them returns a non-cancel error.
func Run(ctx context.Context, cfg Config) error {
	if err := cfg.Mode.Validate(); err != nil {
		return err
	}
	if cfg.RoundClock == nil {
		return errors.New("lifecycle: RoundClock is required")
	}
	if cfg.Mode.HasRoundInit() && cfg.RoundInit == nil {
		return errors.New("lifecycle: RoundInit service is required for round-init mode")
	}
	if cfg.Mode.HasReward() && cfg.Reward == nil {
		return errors.New("lifecycle: Reward service is required for reward mode")
	}

	var wg sync.WaitGroup
	errs := make(chan error, 3)

	if cfg.Mode.HasRoundInit() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cfg.RoundInit.Run(ctx, cfg.RoundClock); err != nil && !errors.Is(err, context.Canceled) {
				errs <- fmt.Errorf("round-init: %w", err)
			}
		}()
	}
	if cfg.Mode.HasReward() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cfg.Reward.Run(ctx, cfg.RoundClock); err != nil && !errors.Is(err, context.Canceled) {
				errs <- fmt.Errorf("reward: %w", err)
			}
		}()
	}
	if cfg.Listener != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cfg.Listener.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
				errs <- fmt.Errorf("grpc: %w", err)
			}
		}()
	}

	if cfg.Logger != nil {
		cfg.Logger.Info("lifecycle.started",
			logger.String("mode", cfg.Mode.String()),
		)
	}

	// Wait for ctx cancel OR first error.
	select {
	case <-ctx.Done():
		// ctx cancellation flows down to the services via the same ctx.
	case err := <-errs:
		if cfg.Logger != nil {
			cfg.Logger.Error("lifecycle.service_error", logger.Err(err))
		}
		// Drain the rest by waiting; ctx cancel will propagate on caller's
		// side.
		wg.Wait()
		return err
	}
	wg.Wait()
	if cfg.Logger != nil {
		cfg.Logger.Info("lifecycle.stopped")
	}
	return nil
}
