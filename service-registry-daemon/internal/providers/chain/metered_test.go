package chain

import (
	"context"
	"errors"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

func TestMeteredChain_GetOK(t *testing.T) {
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	rec := metrics.NewCounter()
	inner := NewInMemory(addr)
	inner.PreLoad(addr, "https://x")
	c := WithMetrics(inner, rec)
	if _, err := c.GetServiceURI(context.Background(), addr); err != nil {
		t.Fatal(err)
	}
	if rec.ChainReads.Load() != 1 {
		t.Fatal("ChainReads")
	}
	if got := rec.LastChainOutcome.Load(); got != metrics.OutcomeOK {
		t.Fatalf("last outcome = %v", got)
	}
}

func TestMeteredChain_GetNotFound(t *testing.T) {
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	rec := metrics.NewCounter()
	c := WithMetrics(NewInMemory(addr), rec)
	if _, err := c.GetServiceURI(context.Background(), addr); !errors.Is(err, types.ErrNotFound) {
		t.Fatal(err)
	}
	if got := rec.LastChainOutcome.Load(); got != metrics.OutcomeNotFound {
		t.Fatalf("last outcome = %v", got)
	}
}

func TestWithMetrics_NilRecorderReturnsInner(t *testing.T) {
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	inner := NewInMemory(addr)
	if got := WithMetrics(inner, nil); got != inner {
		t.Fatal("nil recorder should return the inner chain unchanged")
	}
}
