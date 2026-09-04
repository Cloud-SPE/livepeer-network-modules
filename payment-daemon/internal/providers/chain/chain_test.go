package chain

import (
	"context"
	"errors"
	"strings"
	"testing"

	cchain "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	chaintesting "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing"
)

func TestCheckChainID(t *testing.T) {
	ctx := context.Background()

	t.Run("matches", func(t *testing.T) {
		f := chaintesting.NewFakeRPC() // 42161 by default
		if err := CheckChainID(ctx, f, ArbitrumOneChainID); err != nil {
			t.Fatalf("unexpected: %v", err)
		}
	})
	t.Run("mismatch names both sides", func(t *testing.T) {
		f := chaintesting.NewFakeRPC()
		f.DefaultChainID = cchain.ChainID(1)
		err := CheckChainID(ctx, f, ArbitrumOneChainID)
		if err == nil || !strings.Contains(err.Error(), "rpc=1, expected=42161") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("zero disables the check", func(t *testing.T) {
		f := chaintesting.NewFakeRPC()
		f.DefaultChainID = cchain.ChainID(1)
		if err := CheckChainID(ctx, f, 0); err != nil {
			t.Fatalf("expected=0 must skip: %v", err)
		}
	})
	t.Run("rpc error is surfaced", func(t *testing.T) {
		f := chaintesting.NewFakeRPC()
		f.InjectError("ChainID", errors.New("connection refused"))
		err := CheckChainID(ctx, f, ArbitrumOneChainID)
		if err == nil || !strings.Contains(err.Error(), "eth_chainId") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("nil client", func(t *testing.T) {
		if err := CheckChainID(ctx, nil, ArbitrumOneChainID); err == nil {
			t.Fatal("nil client must error")
		}
	})
}
