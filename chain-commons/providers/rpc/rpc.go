// Package rpc provides the multi-URL Ethereum RPC abstraction with
// circuit-breaker failover.
//
// The interface mirrors a subset of go-ethereum's ethclient.Client surface,
// scoped to what chain-commons services need. Implementations live in
// subpackages: providers/rpc/multi (production multi-URL impl).
//
// See docs/design-docs/multi-rpc-failover.md for the full design.
package rpc

import (
	"context"
	"math/big"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// RPC is the Ethereum RPC abstraction.
type RPC interface {
	// Read-only contract calls.
	CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
	PendingCallContract(ctx context.Context, msg ethereum.CallMsg) ([]byte, error)
	CodeAt(ctx context.Context, addr chain.Address, blockNumber *big.Int) ([]byte, error)

	// Transaction submission and tracking.
	SendTransaction(ctx context.Context, tx *types.Transaction) error
	TransactionByHash(ctx context.Context, hash chain.TxHash) (*types.Transaction, bool, error)
	TransactionReceipt(ctx context.Context, hash chain.TxHash) (*types.Receipt, error)

	// Block and header access.
	BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error)
	HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error)

	// Log filtering.
	FilterLogs(ctx context.Context, query ethereum.FilterQuery) ([]types.Log, error)

	// Account-state queries.
	PendingNonceAt(ctx context.Context, addr chain.Address) (uint64, error)
	BalanceAt(ctx context.Context, addr chain.Address, blockNumber *big.Int) (*big.Int, error)

	// Gas oracle (raw RPC; the gasoracle provider wraps this with TTL caching).
	SuggestGasPrice(ctx context.Context) (*big.Int, error)
	SuggestGasTipCap(ctx context.Context) (*big.Int, error)

	// Network identity.
	ChainID(ctx context.Context) (chain.ChainID, error)

	// Close releases all underlying connections.
	Close() error
}

// EndpointInfo reports per-URL state for ops dashboards. Implementations
// expose this so daemons can render circuit-breaker status without dialing
// the upstreams themselves.
type EndpointInfo struct {
	URL                 string
	Role                string // "primary" | "backup"
	CircuitState        string // "closed" | "half-open" | "open"
	ConsecutiveFailures int
	LastSuccessUnix     int64
	LastFailureUnix     int64
}

// Inspector is implemented by RPC clients that expose endpoint state.
// Optional; consumers that don't care don't need to type-assert for it.
type Inspector interface {
	Endpoints() []EndpointInfo
}

// Compile-time assertion helpers for downstream tests/mocks.
var _ = common.HexToAddress
