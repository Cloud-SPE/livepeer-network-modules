package orchstatus

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	chaintesting "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"
)

type stubRegistry struct {
	uri string
	err error
}

func (s *stubRegistry) GetServiceURI(_ context.Context, _ chain.Address) (string, error) {
	return s.uri, s.err
}

func TestNewValidates(t *testing.T) {
	addr := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	rpcFake := chaintesting.NewFakeRPC()
	reg := &stubRegistry{}

	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "missing registry", cfg: Config{}},
		{name: "missing rpc", cfg: Config{Registry: reg}},
		{name: "missing orch address", cfg: Config{Registry: reg, RPC: rpcFake}},
		{name: "missing wallet address", cfg: Config{Registry: reg, RPC: rpcFake, OrchAddress: addr}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}

	if _, err := New(Config{Registry: reg, RPC: rpcFake, OrchAddress: addr, WalletAddress: addr}); err != nil {
		t.Fatalf("New: %v", err)
	}
}

func TestGetOnChainServiceURIAndRegistration(t *testing.T) {
	addr := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	rpcFake := chaintesting.NewFakeRPC()
	svc, err := New(Config{
		Registry:      &stubRegistry{uri: "https://orch.example.com"},
		RPC:           rpcFake,
		OrchAddress:   addr,
		WalletAddress: addr,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetOnChainServiceURI(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://orch.example.com" {
		t.Fatalf("uri = %q", got)
	}
	registered, err := svc.IsRegistered(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !registered {
		t.Fatal("expected registered")
	}
}

func TestIsRegisteredFalseOnEmptyURI(t *testing.T) {
	addr := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	rpcFake := chaintesting.NewFakeRPC()
	svc, err := New(Config{
		Registry:      &stubRegistry{uri: "  "},
		RPC:           rpcFake,
		OrchAddress:   addr,
		WalletAddress: addr,
	})
	if err != nil {
		t.Fatal(err)
	}
	registered, err := svc.IsRegistered(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if registered {
		t.Fatal("expected unregistered")
	}
}

func TestGetWalletBalance(t *testing.T) {
	addr := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	rpcFake := chaintesting.NewFakeRPC()
	rpcFake.BalanceAtFunc = func(_ context.Context, got chain.Address, _ *big.Int) (*big.Int, error) {
		if got != addr {
			t.Fatalf("wallet = %s; want %s", got.Hex(), addr.Hex())
		}
		return big.NewInt(12345), nil
	}
	svc, err := New(Config{
		Registry:      &stubRegistry{},
		RPC:           rpcFake,
		OrchAddress:   addr,
		WalletAddress: addr,
	})
	if err != nil {
		t.Fatal(err)
	}
	wallet, bal, err := svc.GetWalletBalance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if wallet != addr {
		t.Fatalf("wallet = %s; want %s", wallet.Hex(), addr.Hex())
	}
	if bal.Cmp(big.NewInt(12345)) != 0 {
		t.Fatalf("balance = %s", bal.String())
	}
}

func TestRegistryErrorPropagates(t *testing.T) {
	addr := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	rpcFake := chaintesting.NewFakeRPC()
	svc, err := New(Config{
		Registry:      &stubRegistry{err: errors.New("boom")},
		RPC:           rpcFake,
		OrchAddress:   addr,
		WalletAddress: addr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetOnChainServiceURI(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if _, err := svc.IsRegistered(context.Background()); err == nil {
		t.Fatal("expected registration error")
	}
}

func TestGetWalletBalanceErrorPropagates(t *testing.T) {
	addr := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	rpcFake := chaintesting.NewFakeRPC()
	rpcFake.BalanceAtFunc = func(_ context.Context, _ chain.Address, _ *big.Int) (*big.Int, error) {
		return nil, errors.New("balance boom")
	}
	svc, err := New(Config{
		Registry:      &stubRegistry{},
		RPC:           rpcFake,
		OrchAddress:   addr,
		WalletAddress: addr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.GetWalletBalance(context.Background()); err == nil {
		t.Fatal("expected balance error")
	}
}
