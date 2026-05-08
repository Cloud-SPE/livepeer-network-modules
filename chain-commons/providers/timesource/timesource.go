// Package timesource provides on-chain time abstraction: current round,
// current L1 block, and typed Round event subscriptions.
//
// Implementation wraps providers/rpc to call RoundsManager.currentRound()
// and eth_getBlockByNumber("latest") on Config.BlockPollInterval. The
// l1BlockNumber field is parsed from the L2 block header (Arbitrum Nitro
// adds it).
package timesource

import (
	"context"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
)

// TimeSource is the on-chain time abstraction.
type TimeSource interface {
	// CurrentRound returns the protocol round currently active.
	CurrentRound(ctx context.Context) (chain.Round, error)

	// CurrentL1Block returns the Arbitrum-recorded L1 block from the latest
	// L2 block header.
	CurrentL1Block(ctx context.Context) (chain.BlockNumber, error)

	// SubscribeRounds returns a channel that receives a typed Round event
	// every time the active round changes. The channel receives the current
	// round on subscribe so consumers don't miss the first event.
	SubscribeRounds(ctx context.Context) (<-chan chain.Round, error)

	// SubscribeL1Blocks returns a channel that receives every observed L1
	// block number transition. Higher-frequency than rounds; useful for
	// reward/round-init triggers that operate on L1 block boundaries.
	SubscribeL1Blocks(ctx context.Context) (<-chan chain.BlockNumber, error)
}
