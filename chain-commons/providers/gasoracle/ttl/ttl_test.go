package ttl_test

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/gasoracle/ttl"
	chaintest "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"
)

func TestNew_RequiresRPC(t *testing.T) {
	if _, err := ttl.New(ttl.Options{}); err == nil {
		t.Errorf("New without RPC should fail")
	}
}

func TestSuggest_FetchAndCache(t *testing.T) {
	rpc := chaintest.NewFakeRPC()
	clock := chaintest.NewFakeClock(time.Time{})
	oracle, err := ttl.New(ttl.Options{
		RPC:   rpc,
		TTL:   100 * time.Millisecond,
		Clock: clock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// First call: hits RPC.
	e1, err := oracle.Suggest(context.Background())
	if err != nil {
		t.Fatalf("Suggest 1: %v", err)
	}
	if e1.Source != "rpc" {
		t.Errorf("first suggest source = %q, want rpc", e1.Source)
	}
	if rpc.CallCount("SuggestGasPrice") != 1 {
		t.Errorf("expected 1 SuggestGasPrice call, got %d", rpc.CallCount("SuggestGasPrice"))
	}

	// Second call within TTL: returns cached.
	e2, err := oracle.Suggest(context.Background())
	if err != nil {
		t.Fatalf("Suggest 2: %v", err)
	}
	if e2.Source != "cache" {
		t.Errorf("second suggest source = %q, want cache", e2.Source)
	}
	if rpc.CallCount("SuggestGasPrice") != 1 {
		t.Errorf("cache hit should not call RPC again")
	}

	// Advance clock past TTL: refresh.
	clock.Advance(200 * time.Millisecond)
	e3, _ := oracle.Suggest(context.Background())
	if e3.Source != "rpc" {
		t.Errorf("post-TTL suggest source = %q, want rpc", e3.Source)
	}
}

func TestSuggest_AppliesMaxClamp(t *testing.T) {
	rpc := chaintest.NewFakeRPC()
	rpc.SuggestGasPriceFunc = func(_ context.Context) (*big.Int, error) {
		return big.NewInt(100_000_000_000), nil // 100 gwei base
	}
	rpc.SuggestGasTipCapFunc = func(_ context.Context) (*big.Int, error) {
		return big.NewInt(2_000_000_000), nil // 2 gwei tip
	}
	max := big.NewInt(50_000_000_000) // 50 gwei ceiling

	oracle, _ := ttl.New(ttl.Options{
		RPC: rpc,
		Max: max,
	})
	e, err := oracle.Suggest(context.Background())
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if e.FeeCap.Cmp(max) != 0 {
		t.Errorf("FeeCap = %s, want clamped to Max=%s", e.FeeCap, max)
	}
}

func TestSuggest_AppliesMinClamp(t *testing.T) {
	rpc := chaintest.NewFakeRPC()
	rpc.SuggestGasPriceFunc = func(_ context.Context) (*big.Int, error) {
		return big.NewInt(1_000_000), nil // very low
	}
	rpc.SuggestGasTipCapFunc = func(_ context.Context) (*big.Int, error) {
		return big.NewInt(1_000), nil
	}
	min := big.NewInt(100_000_000_000) // 100 gwei floor

	oracle, _ := ttl.New(ttl.Options{
		RPC: rpc,
		Min: min,
	})
	e, _ := oracle.Suggest(context.Background())
	if e.FeeCap.Cmp(min) != 0 {
		t.Errorf("FeeCap = %s, want clamped to Min=%s", e.FeeCap, min)
	}
}

func TestSuggest_RPCError(t *testing.T) {
	rpc := chaintest.NewFakeRPC()
	rpc.InjectError("SuggestGasPrice", errors.New("rpc down"))

	oracle, _ := ttl.New(ttl.Options{RPC: rpc})
	if _, err := oracle.Suggest(context.Background()); err == nil {
		t.Errorf("Suggest with RPC down should fail")
	}
}

func TestSuggest_TipCapFallback(t *testing.T) {
	rpc := chaintest.NewFakeRPC()
	rpc.InjectErrorN("SuggestGasTipCap", errors.New("not supported"), 100)

	oracle, _ := ttl.New(ttl.Options{RPC: rpc})
	e, err := oracle.Suggest(context.Background())
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if e.TipCap == nil || e.TipCap.Sign() == 0 {
		t.Errorf("TipCap fallback should be non-zero, got %v", e.TipCap)
	}
}

func TestSuggestTipCap_Caches(t *testing.T) {
	rpc := chaintest.NewFakeRPC()
	clock := chaintest.NewFakeClock(time.Time{})
	oracle, _ := ttl.New(ttl.Options{
		RPC:   rpc,
		TTL:   100 * time.Millisecond,
		Clock: clock,
	})

	_, _ = oracle.SuggestTipCap(context.Background())
	_, _ = oracle.SuggestTipCap(context.Background())
	if rpc.CallCount("SuggestGasTipCap") != 1 {
		t.Errorf("tip-cap cache not honored: got %d calls", rpc.CallCount("SuggestGasTipCap"))
	}

	clock.Advance(200 * time.Millisecond)
	_, _ = oracle.SuggestTipCap(context.Background())
	if rpc.CallCount("SuggestGasTipCap") != 2 {
		t.Errorf("tip-cap should refresh post-TTL: got %d calls", rpc.CallCount("SuggestGasTipCap"))
	}
}

func TestSuggestTipCap_RPCError(t *testing.T) {
	rpc := chaintest.NewFakeRPC()
	rpc.InjectError("SuggestGasTipCap", errors.New("rpc down"))

	oracle, _ := ttl.New(ttl.Options{RPC: rpc})
	if _, err := oracle.SuggestTipCap(context.Background()); err == nil {
		t.Errorf("SuggestTipCap with RPC down should fail")
	}
}
