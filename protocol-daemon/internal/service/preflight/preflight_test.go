package preflight

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/controller"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/gasoracle"
	chaintesting "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/types"
)

// fakeGasOracle is a minimal gasoracle.GasOracle for tests.
type fakeGasOracle struct {
	suggest gasoracle.Estimate
	err     error
}

func (f *fakeGasOracle) Suggest(_ context.Context) (gasoracle.Estimate, error) {
	if f.err != nil {
		return gasoracle.Estimate{}, f.err
	}
	return f.suggest, nil
}
func (f *fakeGasOracle) SuggestTipCap(_ context.Context) (chain.Wei, error) {
	return big.NewInt(0), nil
}

func ok(t *testing.T) Config {
	t.Helper()
	rpc := chaintesting.NewFakeRPC()
	rpc.DefaultBalance = big.NewInt(1e18)
	rm := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	bm := common.HexToAddress("0x000000000000000000000000000000000000FB01")
	ctrl := chaintesting.NewFakeController(controller.Addresses{
		RoundsManager:  rm,
		BondingManager: bm,
	}, nil)
	ks := chaintesting.NewFakeKeystore("preflight-test-seed")
	gas := &fakeGasOracle{suggest: gasoracle.Estimate{Source: "rpc"}}
	return Config{
		RPC:           rpc,
		Controller:    ctrl,
		Keystore:      ks,
		GasOracle:     gas,
		ExpectedChain: 42161,
		MinBalanceWei: big.NewInt(0),
		Mode:          types.ModeBoth,
	}
}

func TestRunHappyPath(t *testing.T) {
	cfg := ok(t)
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.ChainID != 42161 {
		t.Fatalf("ChainID = %d", res.ChainID)
	}
	if res.RoundsManager == (chain.Address{}) {
		t.Fatal("RoundsManager not stamped")
	}
}

func TestRunRequiresProviders(t *testing.T) {
	if _, err := Run(context.Background(), Config{}); err == nil {
		t.Fatal("expected error: no RPC")
	}
	cfg := Config{RPC: chaintesting.NewFakeRPC()}
	if _, err := Run(context.Background(), cfg); err == nil {
		t.Fatal("expected error: no controller")
	}
}

func TestChainIDMismatch(t *testing.T) {
	cfg := ok(t)
	cfg.ExpectedChain = 1
	_, err := Run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), types.ErrCodePreflightChainID) {
		t.Fatalf("expected chain-id error; got %v", err)
	}
}

func TestChainIDError(t *testing.T) {
	cfg := ok(t)
	rpc := cfg.RPC.(*chaintesting.FakeRPC)
	rpc.ChainIDFunc = func(_ context.Context) (chain.ChainID, error) {
		return 0, errors.New("rpc broken")
	}
	if _, err := Run(context.Background(), cfg); err == nil {
		t.Fatal("expected error")
	}
}

func TestRoundsManagerEmpty(t *testing.T) {
	cfg := ok(t)
	ctrl := cfg.Controller.(*chaintesting.FakeController)
	ctrl.SetAddress("RoundsManager", chain.Address{})
	_, err := Run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), types.ErrCodePreflightControllerEmpty) {
		t.Fatalf("expected controller-empty error; got %v", err)
	}
}

func TestBondingManagerEmptyInRewardMode(t *testing.T) {
	cfg := ok(t)
	cfg.Mode = types.ModeReward
	ctrl := cfg.Controller.(*chaintesting.FakeController)
	ctrl.SetAddress("BondingManager", chain.Address{})
	_, err := Run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), types.ErrCodePreflightControllerEmpty) {
		t.Fatalf("expected error; got %v", err)
	}
}

func TestBondingManagerEmptyOKInRoundInitMode(t *testing.T) {
	cfg := ok(t)
	cfg.Mode = types.ModeRoundInit
	ctrl := cfg.Controller.(*chaintesting.FakeController)
	ctrl.SetAddress("BondingManager", chain.Address{})
	if _, err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("round-init mode tolerates empty BondingManager: %v", err)
	}
}

func TestRoundsManagerNoCode(t *testing.T) {
	cfg := ok(t)
	rpc := cfg.RPC.(*chaintesting.FakeRPC)
	rpc.CodeAtFunc = func(_ context.Context, _ chain.Address, _ *big.Int) ([]byte, error) {
		return nil, nil
	}
	_, err := Run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), types.ErrCodePreflightContractCode) {
		t.Fatalf("expected contract-code error; got %v", err)
	}
}

func TestBalanceBelowMin(t *testing.T) {
	cfg := ok(t)
	cfg.MinBalanceWei = big.NewInt(int64(1e18))
	rpc := cfg.RPC.(*chaintesting.FakeRPC)
	rpc.BalanceAtFunc = func(_ context.Context, _ chain.Address, _ *big.Int) (*big.Int, error) {
		return big.NewInt(1), nil
	}
	_, err := Run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), types.ErrCodePreflightBalance) {
		t.Fatalf("expected balance error; got %v", err)
	}
}

func TestBalanceCallError(t *testing.T) {
	cfg := ok(t)
	rpc := cfg.RPC.(*chaintesting.FakeRPC)
	rpc.BalanceAtFunc = func(_ context.Context, _ chain.Address, _ *big.Int) (*big.Int, error) {
		return nil, errors.New("rpc broken")
	}
	_, err := Run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), types.ErrCodePreflightBalance) {
		t.Fatalf("expected balance error; got %v", err)
	}
}

func TestGasOracleError(t *testing.T) {
	cfg := ok(t)
	cfg.GasOracle = &fakeGasOracle{err: errors.New("oracle dead")}
	_, err := Run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), types.ErrCodePreflightGasOracle) {
		t.Fatalf("expected gas-oracle error; got %v", err)
	}
}

func TestHotColdSplitDetected(t *testing.T) {
	cfg := ok(t)
	cfg.Mode = types.ModeReward
	cfg.OrchAddress = common.HexToAddress("0x00000000000000000000000000000000000000A1")
	if _, err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
}

func TestRunWithLogger(t *testing.T) {
	cfg := ok(t)
	cfg.Logger = chaintesting.NewFakeLogger()
	if _, err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
}
