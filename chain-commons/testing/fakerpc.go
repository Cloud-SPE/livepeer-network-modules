package chaintesting

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/rpc"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
)

// FakeRPC is a programmable rpc.RPC for tests. Each method has a
// corresponding "*Func" field; if nil, the method returns a sane default
// (zero values, stubbed receipts) so tests can configure only the methods
// they care about.
//
// Concurrency-safe: methods can be invoked concurrently. The "*Func"
// callbacks are read under a single mutex; they themselves run outside
// the lock so they can perform their own concurrency-safe operations.
//
// Fault injection:
//   - InjectError(method, err): the next call to method returns err.
//   - InjectErrorN(method, err, n): the next n calls return err.
//   - InjectLatency(d): every call sleeps d before responding (after
//     the configured func runs).
type FakeRPC struct {
	mu sync.Mutex

	CallContractFunc        func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
	PendingCallContractFunc func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error)
	CodeAtFunc              func(ctx context.Context, addr chain.Address, blockNumber *big.Int) ([]byte, error)

	SendTransactionFunc    func(ctx context.Context, tx *types.Transaction) error
	TransactionByHashFunc  func(ctx context.Context, hash chain.TxHash) (*types.Transaction, bool, error)
	TransactionReceiptFunc func(ctx context.Context, hash chain.TxHash) (*types.Receipt, error)

	BlockByNumberFunc  func(ctx context.Context, number *big.Int) (*types.Block, error)
	HeaderByNumberFunc func(ctx context.Context, number *big.Int) (*types.Header, error)

	FilterLogsFunc func(ctx context.Context, query ethereum.FilterQuery) ([]types.Log, error)

	PendingNonceAtFunc func(ctx context.Context, addr chain.Address) (uint64, error)
	BalanceAtFunc      func(ctx context.Context, addr chain.Address, blockNumber *big.Int) (*big.Int, error)

	SuggestGasPriceFunc  func(ctx context.Context) (*big.Int, error)
	SuggestGasTipCapFunc func(ctx context.Context) (*big.Int, error)
	ChainIDFunc          func(ctx context.Context) (chain.ChainID, error)

	// Defaults used when a func is nil.
	DefaultChainID chain.ChainID
	DefaultBalance *big.Int
	DefaultNonce   uint64

	// Fault injection state.
	injectedErrors map[string][]error
	injectedLatency time.Duration

	// Bookkeeping.
	calls map[string]int
}

// NewFakeRPC returns a FakeRPC with sensible defaults.
func NewFakeRPC() *FakeRPC {
	return &FakeRPC{
		DefaultChainID: 42161,
		DefaultBalance: big.NewInt(0),
		DefaultNonce:   0,
		injectedErrors: make(map[string][]error),
		calls:          make(map[string]int),
	}
}

// CallCount returns how many times the named method has been invoked.
func (r *FakeRPC) CallCount(method string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[method]
}

// InjectError causes the next call to method to return err.
func (r *FakeRPC) InjectError(method string, err error) {
	r.InjectErrorN(method, err, 1)
}

// InjectErrorN causes the next n calls to method to return err.
func (r *FakeRPC) InjectErrorN(method string, err error, n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := 0; i < n; i++ {
		r.injectedErrors[method] = append(r.injectedErrors[method], err)
	}
}

// InjectLatency causes every subsequent call to sleep d before returning.
// Pass 0 to disable.
func (r *FakeRPC) InjectLatency(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.injectedLatency = d
}

// Close is a no-op for FakeRPC.
func (r *FakeRPC) Close() error { return nil }

// nextInjectedError pops one error from the queue for method, if any.
func (r *FakeRPC) nextInjectedError(method string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls[method]++
	q := r.injectedErrors[method]
	if len(q) == 0 {
		return nil
	}
	err := q[0]
	r.injectedErrors[method] = q[1:]
	return err
}

func (r *FakeRPC) maybeLatency(ctx context.Context) error {
	r.mu.Lock()
	d := r.injectedLatency
	r.mu.Unlock()
	if d == 0 {
		return nil
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// CallContract implements rpc.RPC.
func (r *FakeRPC) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	if err := r.nextInjectedError("CallContract"); err != nil {
		return nil, err
	}
	if err := r.maybeLatency(ctx); err != nil {
		return nil, err
	}
	if r.CallContractFunc != nil {
		return r.CallContractFunc(ctx, msg, blockNumber)
	}
	return nil, nil
}

// PendingCallContract implements rpc.RPC.
func (r *FakeRPC) PendingCallContract(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
	if err := r.nextInjectedError("PendingCallContract"); err != nil {
		return nil, err
	}
	if err := r.maybeLatency(ctx); err != nil {
		return nil, err
	}
	if r.PendingCallContractFunc != nil {
		return r.PendingCallContractFunc(ctx, msg)
	}
	return nil, nil
}

// CodeAt implements rpc.RPC.
func (r *FakeRPC) CodeAt(ctx context.Context, addr chain.Address, blockNumber *big.Int) ([]byte, error) {
	if err := r.nextInjectedError("CodeAt"); err != nil {
		return nil, err
	}
	if err := r.maybeLatency(ctx); err != nil {
		return nil, err
	}
	if r.CodeAtFunc != nil {
		return r.CodeAtFunc(ctx, addr, blockNumber)
	}
	// Default: contract code "exists" so preflight passes.
	return []byte{0x60, 0x60, 0x60, 0x40}, nil
}

// SendTransaction implements rpc.RPC.
func (r *FakeRPC) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	if err := r.nextInjectedError("SendTransaction"); err != nil {
		return err
	}
	if err := r.maybeLatency(ctx); err != nil {
		return err
	}
	if r.SendTransactionFunc != nil {
		return r.SendTransactionFunc(ctx, tx)
	}
	return nil
}

// TransactionByHash implements rpc.RPC.
func (r *FakeRPC) TransactionByHash(ctx context.Context, hash chain.TxHash) (*types.Transaction, bool, error) {
	if err := r.nextInjectedError("TransactionByHash"); err != nil {
		return nil, false, err
	}
	if err := r.maybeLatency(ctx); err != nil {
		return nil, false, err
	}
	if r.TransactionByHashFunc != nil {
		return r.TransactionByHashFunc(ctx, hash)
	}
	return nil, false, fmt.Errorf("not found")
}

// TransactionReceipt implements rpc.RPC.
func (r *FakeRPC) TransactionReceipt(ctx context.Context, hash chain.TxHash) (*types.Receipt, error) {
	if err := r.nextInjectedError("TransactionReceipt"); err != nil {
		return nil, err
	}
	if err := r.maybeLatency(ctx); err != nil {
		return nil, err
	}
	if r.TransactionReceiptFunc != nil {
		return r.TransactionReceiptFunc(ctx, hash)
	}
	return nil, ethereum.NotFound
}

// BlockByNumber implements rpc.RPC.
func (r *FakeRPC) BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
	if err := r.nextInjectedError("BlockByNumber"); err != nil {
		return nil, err
	}
	if err := r.maybeLatency(ctx); err != nil {
		return nil, err
	}
	if r.BlockByNumberFunc != nil {
		return r.BlockByNumberFunc(ctx, number)
	}
	return nil, ethereum.NotFound
}

// HeaderByNumber implements rpc.RPC.
func (r *FakeRPC) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	if err := r.nextInjectedError("HeaderByNumber"); err != nil {
		return nil, err
	}
	if err := r.maybeLatency(ctx); err != nil {
		return nil, err
	}
	if r.HeaderByNumberFunc != nil {
		return r.HeaderByNumberFunc(ctx, number)
	}
	// Default: zero header — sufficient for many tests.
	return &types.Header{Number: big.NewInt(0)}, nil
}

// FilterLogs implements rpc.RPC.
func (r *FakeRPC) FilterLogs(ctx context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
	if err := r.nextInjectedError("FilterLogs"); err != nil {
		return nil, err
	}
	if err := r.maybeLatency(ctx); err != nil {
		return nil, err
	}
	if r.FilterLogsFunc != nil {
		return r.FilterLogsFunc(ctx, query)
	}
	return nil, nil
}

// PendingNonceAt implements rpc.RPC.
func (r *FakeRPC) PendingNonceAt(ctx context.Context, addr chain.Address) (uint64, error) {
	if err := r.nextInjectedError("PendingNonceAt"); err != nil {
		return 0, err
	}
	if err := r.maybeLatency(ctx); err != nil {
		return 0, err
	}
	if r.PendingNonceAtFunc != nil {
		return r.PendingNonceAtFunc(ctx, addr)
	}
	return r.DefaultNonce, nil
}

// BalanceAt implements rpc.RPC.
func (r *FakeRPC) BalanceAt(ctx context.Context, addr chain.Address, blockNumber *big.Int) (*big.Int, error) {
	if err := r.nextInjectedError("BalanceAt"); err != nil {
		return nil, err
	}
	if err := r.maybeLatency(ctx); err != nil {
		return nil, err
	}
	if r.BalanceAtFunc != nil {
		return r.BalanceAtFunc(ctx, addr, blockNumber)
	}
	if r.DefaultBalance != nil {
		return new(big.Int).Set(r.DefaultBalance), nil
	}
	return big.NewInt(0), nil
}

// SuggestGasPrice implements rpc.RPC.
func (r *FakeRPC) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	if err := r.nextInjectedError("SuggestGasPrice"); err != nil {
		return nil, err
	}
	if err := r.maybeLatency(ctx); err != nil {
		return nil, err
	}
	if r.SuggestGasPriceFunc != nil {
		return r.SuggestGasPriceFunc(ctx)
	}
	// Default: 100 gwei.
	return new(big.Int).SetUint64(100_000_000_000), nil
}

// SuggestGasTipCap implements rpc.RPC.
func (r *FakeRPC) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	if err := r.nextInjectedError("SuggestGasTipCap"); err != nil {
		return nil, err
	}
	if err := r.maybeLatency(ctx); err != nil {
		return nil, err
	}
	if r.SuggestGasTipCapFunc != nil {
		return r.SuggestGasTipCapFunc(ctx)
	}
	// Default: 1 gwei.
	return new(big.Int).SetUint64(1_000_000_000), nil
}

// ChainID implements rpc.RPC.
func (r *FakeRPC) ChainID(ctx context.Context) (chain.ChainID, error) {
	if err := r.nextInjectedError("ChainID"); err != nil {
		return 0, err
	}
	if err := r.maybeLatency(ctx); err != nil {
		return 0, err
	}
	if r.ChainIDFunc != nil {
		return r.ChainIDFunc(ctx)
	}
	return r.DefaultChainID, nil
}

// Compile-time: FakeRPC implements rpc.RPC.
var _ rpc.RPC = (*FakeRPC)(nil)
