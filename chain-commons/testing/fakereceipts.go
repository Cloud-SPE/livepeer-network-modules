package chaintesting

import (
	"context"
	"sync"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/receipts"
)

// FakeReceipts is a programmable receipts.Receipts for tests.
//
// Tests pre-load expected outcomes via Set(txHash, receipt). WaitConfirmed
// returns the configured outcome after the configured delay (or immediately
// if SimulateInstant is true).
type FakeReceipts struct {
	mu              sync.Mutex
	outcomes        map[chain.TxHash]*receipts.Receipt
	errors          map[chain.TxHash]error
	pending         map[chain.TxHash][]chan struct{}
	SimulateInstant bool
	WaitCount       int
}

// NewFakeReceipts returns an empty FakeReceipts with SimulateInstant=true.
func NewFakeReceipts() *FakeReceipts {
	return &FakeReceipts{
		outcomes:        make(map[chain.TxHash]*receipts.Receipt),
		errors:          make(map[chain.TxHash]error),
		pending:         make(map[chain.TxHash][]chan struct{}),
		SimulateInstant: true,
	}
}

// Set installs the outcome that WaitConfirmed will return for txHash.
// Wakes any pending WaitConfirmed callers for the same hash.
func (f *FakeReceipts) Set(txHash chain.TxHash, r *receipts.Receipt) {
	f.mu.Lock()
	f.outcomes[txHash] = r
	chans := f.pending[txHash]
	delete(f.pending, txHash)
	f.mu.Unlock()
	for _, c := range chans {
		close(c)
	}
}

// SetError installs the error that WaitConfirmed will return for txHash.
func (f *FakeReceipts) SetError(txHash chain.TxHash, err error) {
	f.mu.Lock()
	f.errors[txHash] = err
	chans := f.pending[txHash]
	delete(f.pending, txHash)
	f.mu.Unlock()
	for _, c := range chans {
		close(c)
	}
}

// WaitConfirmed implements receipts.Receipts.
//
// Outcomes are consumed-on-read: a Set'd receipt or error is returned to
// the first WaitConfirmed call for that hash and then cleared. Subsequent
// WaitConfirmed calls block until a new Set/SetError. This matches the
// semantic of "each observation is a fresh poll" and lets tests sequence
// outcomes for the same tx hash (e.g. reorg → confirm) when deterministic
// signing produces identical hashes on resubmit.
func (f *FakeReceipts) WaitConfirmed(ctx context.Context, txHash chain.TxHash, _ uint64) (*receipts.Receipt, error) {
	f.mu.Lock()
	f.WaitCount++
	if f.SimulateInstant {
		if r, ok := f.outcomes[txHash]; ok {
			delete(f.outcomes, txHash)
			f.mu.Unlock()
			return r, nil
		}
		if err, ok := f.errors[txHash]; ok {
			delete(f.errors, txHash)
			f.mu.Unlock()
			return nil, err
		}
	}
	// Wait for Set/SetError to wake us.
	wake := make(chan struct{})
	f.pending[txHash] = append(f.pending[txHash], wake)
	f.mu.Unlock()

	select {
	case <-wake:
		f.mu.Lock()
		defer f.mu.Unlock()
		if r, ok := f.outcomes[txHash]; ok {
			delete(f.outcomes, txHash)
			return r, nil
		}
		if err, ok := f.errors[txHash]; ok {
			delete(f.errors, txHash)
			return nil, err
		}
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Compile-time: FakeReceipts implements receipts.Receipts.
var _ receipts.Receipts = (*FakeReceipts)(nil)
