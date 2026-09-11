package main

import (
	"context"
	"testing"
	"time"

	chaintest "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing/simchain"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/payouts"
)

// simEngine routes openEngine at a simulated chain for the test's
// lifetime. Every engine opened by the command under test shares one
// in-memory intent store (a process restart keeps its BoltDB file; the
// fake store stands in for it) and one RPC wrapper, so the test can count
// broadcasts across reconcile iterations.
type simEngine struct {
	sim *simchain.Chain
	rpc *simchain.Wrapper
}

func useSimEngine(t *testing.T) *simEngine {
	t.Helper()
	sim := simchain.New(t)
	f := &simEngine{sim: sim, rpc: simchain.Wrap(sim.RPC())}
	store := chaintest.NewFakeStore()
	prev := openEngine
	openEngine = func(ctx context.Context, cfg config.Executor) (*payouts.Engine, error) {
		return payouts.OpenWith(ctx, cfg, f.rpc, sim.Accounts[0].Keystore, simchain.DevChainID, payouts.Options{
			Store:       store,
			ReceiptPoll: 2 * time.Millisecond,
			ConfirmWait: 2 * time.Second,
		})
	}
	t.Cleanup(func() { openEngine = prev })
	return f
}

// mine commits a block every 2ms until the test ends.
func (f *simEngine) mine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go f.sim.MineUntil(ctx, 1_000_000, func() bool {
		time.Sleep(2 * time.Millisecond)
		return ctx.Err() != nil
	})
}
