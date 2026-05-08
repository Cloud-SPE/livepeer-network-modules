// Package gasoracle provides the gas-pricing abstraction.
//
// Wraps providers/rpc.SuggestGasPrice and SuggestGasTipCap with TTL caching
// and floor/ceiling clamping. The TTL cache means many concurrent
// transactions during a brief window share one underlying RPC call.
package gasoracle

import (
	"context"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
)

// GasOracle is the gas-pricing abstraction.
type GasOracle interface {
	// Suggest returns the current gas-price estimate, possibly cached.
	Suggest(ctx context.Context) (Estimate, error)

	// SuggestTipCap returns the EIP-1559 priority-fee suggestion, possibly cached.
	SuggestTipCap(ctx context.Context) (chain.Wei, error)
}

// Estimate is the gas-price oracle output.
type Estimate struct {
	// BaseFee is the chain's base fee at the time of estimate.
	BaseFee chain.Wei

	// TipCap is the suggested priority fee per gas.
	TipCap chain.Wei

	// FeeCap is the suggested max fee per gas (BaseFee*2 + TipCap, capped at
	// the operator's GasPriceMax). The TxIntent submitter uses this value.
	FeeCap chain.Wei

	// Source identifies whether this came from the upstream RPC ("rpc") or
	// from the in-memory cache ("cache").
	Source string

	// CachedAt is when the underlying RPC call returned (for both Source
	// values; "cache" hits report when the cache was populated).
	CachedAt time.Time
}
