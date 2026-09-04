package simchain

import (
	"context"
	"math/big"
	"sync"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/rpc"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
)

// Wrapper decorates an rpc.RPC with per-method call counting and fault
// injection. It is the "endpoint that fails" half of a failover test: a
// consumer proves its retry or multi-endpoint logic moves on by wrapping
// a working simchain RPC, telling it to fail the first N calls (or every
// call), and asserting on the outcome and the counts.
//
// The wrapper is safe for concurrent use.
type Wrapper struct {
	inner rpc.RPC

	mu       sync.Mutex
	calls    map[string]int
	failLeft int // calls still to fail; -1 = forever
	failErr  error
	failOnly map[string]bool // when non-empty, only these methods fail
}

var _ rpc.RPC = (*Wrapper)(nil)

// Wrap returns a Wrapper around inner.
func Wrap(inner rpc.RPC) *Wrapper {
	return &Wrapper{inner: inner, calls: map[string]int{}}
}

// FailFirst makes the next n calls (across all methods, or only the
// methods named by Only) return err.
func (w *Wrapper) FailFirst(n int, err error) *Wrapper {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.failLeft = n
	w.failErr = err
	return w
}

// FailAlways makes every call return err until Heal is called.
func (w *Wrapper) FailAlways(err error) *Wrapper {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.failLeft = -1
	w.failErr = err
	return w
}

// Only restricts fault injection to the named methods; calls to other
// methods pass through and are still counted.
func (w *Wrapper) Only(methods ...string) *Wrapper {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.failOnly = map[string]bool{}
	for _, m := range methods {
		w.failOnly[m] = true
	}
	return w
}

// Heal clears any pending fault injection.
func (w *Wrapper) Heal() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.failLeft = 0
	w.failErr = nil
}

// Calls returns how many times method was invoked (including calls that
// were failed by injection).
func (w *Wrapper) Calls(method string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls[method]
}

// Failed returns how many injected failures remain (-1 = forever).
func (w *Wrapper) Failed() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.failLeft
}

// before records the call and reports the error to inject, if any.
func (w *Wrapper) before(method string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls[method]++
	if w.failErr == nil || w.failLeft == 0 {
		return nil
	}
	if len(w.failOnly) > 0 && !w.failOnly[method] {
		return nil
	}
	if w.failLeft > 0 {
		w.failLeft--
	}
	return w.failErr
}

func (w *Wrapper) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	if err := w.before("CallContract"); err != nil {
		return nil, err
	}
	return w.inner.CallContract(ctx, msg, blockNumber)
}

func (w *Wrapper) PendingCallContract(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
	if err := w.before("PendingCallContract"); err != nil {
		return nil, err
	}
	return w.inner.PendingCallContract(ctx, msg)
}

func (w *Wrapper) CodeAt(ctx context.Context, addr chain.Address, blockNumber *big.Int) ([]byte, error) {
	if err := w.before("CodeAt"); err != nil {
		return nil, err
	}
	return w.inner.CodeAt(ctx, addr, blockNumber)
}

func (w *Wrapper) EstimateGas(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
	if err := w.before("EstimateGas"); err != nil {
		return 0, err
	}
	return w.inner.EstimateGas(ctx, msg)
}

func (w *Wrapper) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	if err := w.before("SendTransaction"); err != nil {
		return err
	}
	return w.inner.SendTransaction(ctx, tx)
}

func (w *Wrapper) TransactionByHash(ctx context.Context, hash chain.TxHash) (*types.Transaction, bool, error) {
	if err := w.before("TransactionByHash"); err != nil {
		return nil, false, err
	}
	return w.inner.TransactionByHash(ctx, hash)
}

func (w *Wrapper) TransactionReceipt(ctx context.Context, hash chain.TxHash) (*types.Receipt, error) {
	if err := w.before("TransactionReceipt"); err != nil {
		return nil, err
	}
	return w.inner.TransactionReceipt(ctx, hash)
}

func (w *Wrapper) BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
	if err := w.before("BlockByNumber"); err != nil {
		return nil, err
	}
	return w.inner.BlockByNumber(ctx, number)
}

func (w *Wrapper) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	if err := w.before("HeaderByNumber"); err != nil {
		return nil, err
	}
	return w.inner.HeaderByNumber(ctx, number)
}

func (w *Wrapper) FilterLogs(ctx context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
	if err := w.before("FilterLogs"); err != nil {
		return nil, err
	}
	return w.inner.FilterLogs(ctx, query)
}

func (w *Wrapper) PendingNonceAt(ctx context.Context, addr chain.Address) (uint64, error) {
	if err := w.before("PendingNonceAt"); err != nil {
		return 0, err
	}
	return w.inner.PendingNonceAt(ctx, addr)
}

func (w *Wrapper) BalanceAt(ctx context.Context, addr chain.Address, blockNumber *big.Int) (*big.Int, error) {
	if err := w.before("BalanceAt"); err != nil {
		return nil, err
	}
	return w.inner.BalanceAt(ctx, addr, blockNumber)
}

func (w *Wrapper) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	if err := w.before("SuggestGasPrice"); err != nil {
		return nil, err
	}
	return w.inner.SuggestGasPrice(ctx)
}

func (w *Wrapper) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	if err := w.before("SuggestGasTipCap"); err != nil {
		return nil, err
	}
	return w.inner.SuggestGasTipCap(ctx)
}

func (w *Wrapper) ChainID(ctx context.Context) (chain.ChainID, error) {
	if err := w.before("ChainID"); err != nil {
		return 0, err
	}
	return w.inner.ChainID(ctx)
}

func (w *Wrapper) Close() error {
	if err := w.before("Close"); err != nil {
		return err
	}
	return w.inner.Close()
}
