package chaintesting

import (
	"context"
	"math/big"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/gasoracle"
)

// FakeGasOracle is a programmable GasOracle for tests.
type FakeGasOracle struct {
	mu          sync.Mutex
	estimate    gasoracle.Estimate
	tipCap      chain.Wei
	suggestErr  error
	tipCapErr   error
	suggestCnt  int
	tipCapCnt   int
}

// NewFakeGasOracle returns a FakeGasOracle with sensible defaults: BaseFee
// 1 gwei, TipCap 1 gwei, FeeCap 3 gwei.
func NewFakeGasOracle() *FakeGasOracle {
	return &FakeGasOracle{
		estimate: gasoracle.Estimate{
			BaseFee:  big.NewInt(1_000_000_000),
			TipCap:   big.NewInt(1_000_000_000),
			FeeCap:   big.NewInt(3_000_000_000),
			Source:   "rpc",
			CachedAt: time.Now(),
		},
		tipCap: big.NewInt(1_000_000_000),
	}
}

// SetEstimate replaces the estimate returned by Suggest.
func (g *FakeGasOracle) SetEstimate(e gasoracle.Estimate) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.estimate = e
}

// SetTipCap replaces the tip cap returned by SuggestTipCap.
func (g *FakeGasOracle) SetTipCap(c chain.Wei) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tipCap = c
}

// FailNextSuggest causes the next Suggest call to return err.
func (g *FakeGasOracle) FailNextSuggest(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.suggestErr = err
}

// FailNextTipCap causes the next SuggestTipCap call to return err.
func (g *FakeGasOracle) FailNextTipCap(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tipCapErr = err
}

// SuggestCount returns the total number of Suggest calls made.
func (g *FakeGasOracle) SuggestCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.suggestCnt
}

// Suggest implements gasoracle.GasOracle.
func (g *FakeGasOracle) Suggest(_ context.Context) (gasoracle.Estimate, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.suggestCnt++
	if g.suggestErr != nil {
		err := g.suggestErr
		g.suggestErr = nil
		return gasoracle.Estimate{}, err
	}
	// Return a fresh copy so callers mutating Wei pointers don't affect us.
	return gasoracle.Estimate{
		BaseFee:  copyWei(g.estimate.BaseFee),
		TipCap:   copyWei(g.estimate.TipCap),
		FeeCap:   copyWei(g.estimate.FeeCap),
		Source:   g.estimate.Source,
		CachedAt: g.estimate.CachedAt,
	}, nil
}

// SuggestTipCap implements gasoracle.GasOracle.
func (g *FakeGasOracle) SuggestTipCap(_ context.Context) (chain.Wei, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tipCapCnt++
	if g.tipCapErr != nil {
		err := g.tipCapErr
		g.tipCapErr = nil
		return nil, err
	}
	return copyWei(g.tipCap), nil
}

func copyWei(w chain.Wei) chain.Wei {
	if w == nil {
		return nil
	}
	return new(big.Int).Set(w)
}

// Compile-time: FakeGasOracle implements gasoracle.GasOracle.
var _ gasoracle.GasOracle = (*FakeGasOracle)(nil)
