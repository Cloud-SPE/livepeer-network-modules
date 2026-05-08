package grpc

import (
	"context"
	"encoding/binary"
	"errors"
	"math/big"
	"testing"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/txintent"
	chaintesting "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"
	aiprovider "github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/providers/aiserviceregistry"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/providers/bondingmanager"
	srprovider "github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/providers/serviceregistry"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/repo/poolhints"
	aisrservice "github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/aiserviceregistry"
	orchstatussvc "github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/orchstatus"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/reward"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/roundinit"
	srservice "github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/serviceregistry"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/types"
)

func encodeABIString(v string) []byte {
	paddedLen := ((len(v) + 31) / 32) * 32
	out := make([]byte, 64+paddedLen)
	binary.BigEndian.PutUint64(out[24:32], 32)
	binary.BigEndian.PutUint64(out[56:64], uint64(len(v)))
	copy(out[64:], []byte(v))
	return out
}

// stubRoundsManager mirrors the round-init service interface.
type stubRM struct {
	addr        chain.Address
	initialized bool
}

func (s *stubRM) Address() chain.Address { return s.addr }
func (s *stubRM) CurrentRoundInitialized(_ context.Context) (bool, error) {
	return s.initialized, nil
}
func (s *stubRM) PackInitializeRound() ([]byte, error) {
	return []byte{0x01, 0x02, 0x03, 0x04}, nil
}

// stubBM mirrors the reward service interface.
type stubBM struct {
	addr  chain.Address
	tinfo bondingmanager.TranscoderInfo
	pool  []chain.Address
}

func (s *stubBM) Address() chain.Address { return s.addr }
func (s *stubBM) GetTranscoder(_ context.Context, _ chain.Address) (bondingmanager.TranscoderInfo, error) {
	return s.tinfo, nil
}
func (s *stubBM) GetFirstTranscoderInPool(_ context.Context) (chain.Address, error) {
	if len(s.pool) == 0 {
		return chain.Address{}, nil
	}
	return s.pool[0], nil
}
func (s *stubBM) GetNextTranscoderInPool(_ context.Context, addr chain.Address) (chain.Address, error) {
	for i, p := range s.pool {
		if p == addr {
			if i+1 >= len(s.pool) {
				return chain.Address{}, nil
			}
			return s.pool[i+1], nil
		}
	}
	return chain.Address{}, nil
}
func (s *stubBM) PackRewardWithHint(prev, next chain.Address) ([]byte, error) {
	out := make([]byte, 4+64)
	copy(out[16:], prev[:])
	copy(out[48:], next[:])
	return out, nil
}

// stubSubmitter mimics chain-commons.txintent.Manager.
type stubSubmitter struct {
	intent txintent.TxIntent
	err    error
}

func (s *stubSubmitter) Submit(_ context.Context, p txintent.Params) (txintent.IntentID, error) {
	return txintent.ComputeID(p.Kind, p.KeyParams), nil
}
func (s *stubSubmitter) Status(_ context.Context, id txintent.IntentID) (txintent.TxIntent, error) {
	if s.err != nil {
		return txintent.TxIntent{}, s.err
	}
	cp := s.intent
	cp.ID = id
	return cp, nil
}
func (s *stubSubmitter) Wait(_ context.Context, id txintent.IntentID) (txintent.TxIntent, error) {
	cp := s.intent
	cp.ID = id
	cp.Status = txintent.StatusConfirmed
	return cp, nil
}

func newCache(t *testing.T) reward.PoolHintsCache {
	c, err := poolhints.New(store.Memory())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func newRoundInitSvc(t *testing.T, addr chain.Address) *roundinit.Service {
	t.Helper()
	return newRoundInitSvcWithInit(t, addr, false)
}

func newRoundInitSvcWithInit(t *testing.T, addr chain.Address, initialized bool) *roundinit.Service {
	t.Helper()
	svc, err := roundinit.New(roundinit.Config{
		RoundsManager: &stubRM{addr: addr, initialized: initialized},
		TxIntent:      &stubSubmitter{},
		GasLimit:      1_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func newRewardSvc(t *testing.T, bmAddr, orch chain.Address, tinfo bondingmanager.TranscoderInfo) *reward.Service {
	t.Helper()
	svc, err := reward.New(reward.Config{
		BondingManager: &stubBM{addr: bmAddr, tinfo: tinfo, pool: []chain.Address{orch}},
		TxIntent:       &stubSubmitter{},
		Cache:          newCache(t),
		OrchAddress:    orch,
		GasLimit:       1_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func newServiceRegistrySvc(t *testing.T, addr chain.Address) *srservice.Service {
	t.Helper()
	reg, err := srprovider.New(addr)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := srservice.New(srservice.Config{
		Registry: reg,
		TxIntent: &stubSubmitter{},
		GasLimit: 1_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func newAIServiceRegistrySvc(t *testing.T, addr chain.Address) *aisrservice.Service {
	t.Helper()
	reg, err := aiprovider.New(addr)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := aisrservice.New(aisrservice.Config{
		Registry: reg,
		TxIntent: &stubSubmitter{},
		GasLimit: 1_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func newOrchStatusSvc(t *testing.T, regAddr, orchAddr, walletAddr chain.Address, uri string, balance *big.Int) *orchstatussvc.Service {
	t.Helper()
	rpcFake := chaintesting.NewFakeRPC()
	rpcFake.DefaultBalance = new(big.Int).Set(balance)
	reg, err := srprovider.New(regAddr, rpcFake)
	if err != nil {
		t.Fatal(err)
	}
	rpcFake.CallContractFunc = func(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		return encodeABIString(uri), nil
	}
	svc, err := orchstatussvc.New(orchstatussvc.Config{
		Registry:      reg,
		RPC:           rpcFake,
		OrchAddress:   orchAddr,
		WalletAddress: walletAddr,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestNewValidates(t *testing.T) {
	if _, err := New(Config{Mode: types.Mode("bogus")}); err == nil {
		t.Fatal("expected error on bogus mode")
	}
	if _, err := New(Config{Mode: types.ModeRoundInit}); err == nil {
		t.Fatal("expected error: round-init mode missing service")
	}
	if _, err := New(Config{Mode: types.ModeReward}); err == nil {
		t.Fatal("expected error: reward mode missing service")
	}
}

func TestHealth(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	rs := newRoundInitSvc(t, rmAddr)
	srv, err := New(Config{Mode: types.ModeRoundInit, Version: "v1.0", ChainID: 42161, RoundInit: rs})
	if err != nil {
		t.Fatal(err)
	}
	h, err := srv.Health(context.Background(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if !h.OK || h.Mode != "round-init" || h.Version != "v1.0" || h.ChainID != 42161 {
		t.Fatalf("Health = %+v", h)
	}
}

func TestGetRoundStatusUnimplementedInRewardMode(t *testing.T) {
	bmAddr := common.HexToAddress("0x000000000000000000000000000000000000FB01")
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	rwd := newRewardSvc(t, bmAddr, orch, bondingmanager.TranscoderInfo{Active: true, ActivationRound: 1})

	srv, err := New(Config{Mode: types.ModeReward, Reward: rwd})
	if err != nil {
		t.Fatal(err)
	}
	_, err = srv.GetRoundStatus(context.Background(), struct{}{})
	if !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("expected ErrUnimplemented, got %v", err)
	}
}

func TestGetRewardStatusUnimplementedInRoundInitMode(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	rs := newRoundInitSvc(t, rmAddr)
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs})
	_, err := srv.GetRewardStatus(context.Background(), struct{}{})
	if !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("expected ErrUnimplemented, got %v", err)
	}
}

func TestForceUnimplementedOnWrongMode(t *testing.T) {
	bmAddr := common.HexToAddress("0x000000000000000000000000000000000000FB01")
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	rwd := newRewardSvc(t, bmAddr, orch, bondingmanager.TranscoderInfo{Active: true, ActivationRound: 1})
	srv, _ := New(Config{Mode: types.ModeReward, Reward: rwd})
	if _, err := srv.ForceInitializeRound(context.Background(), struct{}{}); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("expected unimplemented for ForceInitializeRound in reward mode, got %v", err)
	}

	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	rs := newRoundInitSvc(t, rmAddr)
	srv2, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs})
	if _, err := srv2.ForceRewardCall(context.Background(), struct{}{}); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("expected unimplemented for ForceRewardCall in round-init mode, got %v", err)
	}
}

func TestSetServiceURI(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	srAddr := common.HexToAddress("0x000000000000000000000000000000000000FC01")
	rs := newRoundInitSvc(t, rmAddr)
	srv, err := New(Config{Mode: types.ModeRoundInit, RoundInit: rs, Registry: newServiceRegistrySvc(t, srAddr)})
	if err != nil {
		t.Fatal(err)
	}

	out, err := srv.SetServiceURI(context.Background(), SetServiceURIRequest{URL: "https://orch.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if out.ID == (txintent.IntentID{}) {
		t.Fatal("expected intent id")
	}
}

func TestSetServiceURIRequiresRegistry(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	rs := newRoundInitSvc(t, rmAddr)
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs})
	if _, err := srv.SetServiceURI(context.Background(), SetServiceURIRequest{URL: "https://orch.example.com"}); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("expected unimplemented, got %v", err)
	}
}

func TestSetServiceURIRejectsEmpty(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	srAddr := common.HexToAddress("0x000000000000000000000000000000000000FC01")
	rs := newRoundInitSvc(t, rmAddr)
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs, Registry: newServiceRegistrySvc(t, srAddr)})
	if _, err := srv.SetServiceURI(context.Background(), SetServiceURIRequest{}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected invalid argument, got %v", err)
	}
}

func TestSetAIServiceURI(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	aiAddr := common.HexToAddress("0x000000000000000000000000000000000000FC02")
	rs := newRoundInitSvc(t, rmAddr)
	srv, err := New(Config{Mode: types.ModeRoundInit, RoundInit: rs, AIRegistry: newAIServiceRegistrySvc(t, aiAddr)})
	if err != nil {
		t.Fatal(err)
	}

	out, err := srv.SetAIServiceURI(context.Background(), SetAIServiceURIRequest{URL: "https://ai.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if out.ID == (txintent.IntentID{}) {
		t.Fatal("expected intent id")
	}
}

func TestSetAIServiceURIRequiresRegistry(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	rs := newRoundInitSvc(t, rmAddr)
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs})
	if _, err := srv.SetAIServiceURI(context.Background(), SetAIServiceURIRequest{URL: "https://ai.example.com"}); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("expected unimplemented, got %v", err)
	}
}

func TestSetAIServiceURIRejectsEmpty(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	aiAddr := common.HexToAddress("0x000000000000000000000000000000000000FC02")
	rs := newRoundInitSvc(t, rmAddr)
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs, AIRegistry: newAIServiceRegistrySvc(t, aiAddr)})
	if _, err := srv.SetAIServiceURI(context.Background(), SetAIServiceURIRequest{}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected invalid argument, got %v", err)
	}
}

func TestGetOnChainServiceURI(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	regAddr := common.HexToAddress("0x000000000000000000000000000000000000FC01")
	orchAddr := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	walletAddr := common.HexToAddress("0x00000000000000000000000000000000000000B2")
	rs := newRoundInitSvc(t, rmAddr)
	srv, _ := New(Config{
		Mode:      types.ModeRoundInit,
		RoundInit: rs,
		Orch:      newOrchStatusSvc(t, regAddr, orchAddr, walletAddr, "https://orch.example.com", big.NewInt(9)),
	})
	out, err := srv.GetOnChainServiceURI(context.Background(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if out.URL != "https://orch.example.com" {
		t.Fatalf("url = %q", out.URL)
	}
}

func TestIsRegistered(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	regAddr := common.HexToAddress("0x000000000000000000000000000000000000FC01")
	orchAddr := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	walletAddr := common.HexToAddress("0x00000000000000000000000000000000000000B2")
	rs := newRoundInitSvc(t, rmAddr)
	srv, _ := New(Config{
		Mode:      types.ModeRoundInit,
		RoundInit: rs,
		Orch:      newOrchStatusSvc(t, regAddr, orchAddr, walletAddr, "", big.NewInt(9)),
	})
	out, err := srv.IsRegistered(context.Background(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Registered {
		t.Fatal("expected false")
	}
}

func TestGetWalletBalance(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	regAddr := common.HexToAddress("0x000000000000000000000000000000000000FC01")
	orchAddr := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	walletAddr := common.HexToAddress("0x00000000000000000000000000000000000000B2")
	rs := newRoundInitSvc(t, rmAddr)
	srv, _ := New(Config{
		Mode:      types.ModeRoundInit,
		RoundInit: rs,
		Orch:      newOrchStatusSvc(t, regAddr, orchAddr, walletAddr, "", big.NewInt(12345)),
	})
	out, err := srv.GetWalletBalance(context.Background(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if out.WalletAddress != walletAddr {
		t.Fatalf("wallet = %s", out.WalletAddress.Hex())
	}
	if out.BalanceWei.Cmp(big.NewInt(12345)) != 0 {
		t.Fatalf("balance = %s", out.BalanceWei.String())
	}
}

func TestGetOnChainAIServiceURI(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	regAddr := common.HexToAddress("0x000000000000000000000000000000000000FC02")
	orchAddr := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	walletAddr := common.HexToAddress("0x00000000000000000000000000000000000000B2")
	rs := newRoundInitSvc(t, rmAddr)
	srv, _ := New(Config{
		Mode:      types.ModeRoundInit,
		RoundInit: rs,
		AIOrch:    newOrchStatusSvc(t, regAddr, orchAddr, walletAddr, "https://ai.example.com", big.NewInt(9)),
	})
	out, err := srv.GetOnChainAIServiceURI(context.Background(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if out.URL != "https://ai.example.com" {
		t.Fatalf("url = %q", out.URL)
	}
}

func TestIsAIRegistered(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	regAddr := common.HexToAddress("0x000000000000000000000000000000000000FC02")
	orchAddr := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	walletAddr := common.HexToAddress("0x00000000000000000000000000000000000000B2")
	rs := newRoundInitSvc(t, rmAddr)
	srv, _ := New(Config{
		Mode:      types.ModeRoundInit,
		RoundInit: rs,
		AIOrch:    newOrchStatusSvc(t, regAddr, orchAddr, walletAddr, "", big.NewInt(9)),
	})
	out, err := srv.IsAIRegistered(context.Background(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Registered {
		t.Fatal("expected false")
	}
}

func TestOrchStatusMethodsRequireService(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	rs := newRoundInitSvc(t, rmAddr)
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs})
	if _, err := srv.GetOnChainServiceURI(context.Background(), struct{}{}); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("GetOnChainServiceURI: want unimplemented, got %v", err)
	}
	if _, err := srv.IsRegistered(context.Background(), struct{}{}); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("IsRegistered: want unimplemented, got %v", err)
	}
	if _, err := srv.GetWalletBalance(context.Background(), struct{}{}); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("GetWalletBalance: want unimplemented, got %v", err)
	}
	if _, err := srv.GetOnChainAIServiceURI(context.Background(), struct{}{}); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("GetOnChainAIServiceURI: want unimplemented, got %v", err)
	}
	if _, err := srv.IsAIRegistered(context.Background(), struct{}{}); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("IsAIRegistered: want unimplemented, got %v", err)
	}
}

func TestForceInitializeRoundIdempotent(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	rs := newRoundInitSvc(t, rmAddr)
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs})

	r1, err := srv.ForceInitializeRound(context.Background(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := srv.ForceInitializeRound(context.Background(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Submitted == nil || r2.Submitted == nil {
		t.Fatalf("expected Submitted arm both calls; got r1=%+v r2=%+v", r1, r2)
	}
	if r1.Submitted.ID != r2.Submitted.ID {
		t.Fatalf("idempotent ForceInitializeRound: ids differ: %x vs %x", r1.Submitted.ID, r2.Submitted.ID)
	}
}

func TestForceRewardCall(t *testing.T) {
	bmAddr := common.HexToAddress("0x000000000000000000000000000000000000FB01")
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	rwd := newRewardSvc(t, bmAddr, orch, bondingmanager.TranscoderInfo{Active: true, ActivationRound: 1, LastRewardRound: 0})

	// Manually drive the service to round 5 first.
	if _, err := rwd.TryReward(context.Background(), chain.Round{Number: 5}); err != nil {
		t.Fatal(err)
	}

	srv, _ := New(Config{Mode: types.ModeReward, Reward: rwd})
	out, err := srv.ForceRewardCall(context.Background(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	// Stub bondingmanager's TranscoderInfo doesn't track per-round state
	// across TryReward calls — the second call sees the same input and
	// re-submits idempotently. Either Submitted or Skipped is acceptable;
	// what matters is that exactly one arm is set.
	if (out.Submitted == nil) == (out.Skipped == nil) {
		t.Fatalf("expected exactly one arm of ForceOutcome set; got %+v", out)
	}
}

func TestForceRewardCallSkipsAlreadyRewarded(t *testing.T) {
	bmAddr := common.HexToAddress("0x000000000000000000000000000000000000FB01")
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	// Active at round 100, already rewarded at round 100 → AlreadyRewarded
	// path. Drive lastRound to 100 first so ForceRewardCall reads it from
	// Status.
	rwd := newRewardSvc(t, bmAddr, orch, bondingmanager.TranscoderInfo{
		Active: true, ActivationRound: 1, DeactivationRound: 1_000_000, LastRewardRound: 100,
	})
	if _, err := rwd.TryReward(context.Background(), chain.Round{Number: 100}); err != nil {
		t.Fatal(err)
	}

	srv, _ := New(Config{Mode: types.ModeReward, Reward: rwd})
	out, err := srv.ForceRewardCall(context.Background(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Skipped == nil {
		t.Fatalf("expected Skipped arm; got %+v", out)
	}
	if out.Skipped.Code != SkipCodeAlreadyRewarded {
		t.Fatalf("Skipped.Code = %d; want SkipCodeAlreadyRewarded (%d)",
			out.Skipped.Code, SkipCodeAlreadyRewarded)
	}
	if out.Skipped.Reason != "already rewarded this round" {
		t.Fatalf("Skipped.Reason = %q", out.Skipped.Reason)
	}
}

func TestForceInitializeRoundSkipsAlreadyInitialized(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	rs := newRoundInitSvcWithInit(t, rmAddr, true)
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs})

	out, err := srv.ForceInitializeRound(context.Background(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Skipped == nil {
		t.Fatalf("expected Skipped arm; got %+v", out)
	}
	if out.Skipped.Code != SkipCodeRoundInitialized {
		t.Fatalf("Skipped.Code = %d; want SkipCodeRoundInitialized (%d)",
			out.Skipped.Code, SkipCodeRoundInitialized)
	}
	if out.Skipped.Reason != "round already initialized" {
		t.Fatalf("Skipped.Reason = %q", out.Skipped.Reason)
	}
}

func TestGetTxIntent(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	rs := newRoundInitSvc(t, rmAddr)

	stub := &stubSubmitter{
		intent: txintent.TxIntent{
			Kind:   "InitializeRound",
			Status: txintent.StatusConfirmed,
		},
	}
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs, Tx: stub})

	id := txintent.ComputeID("InitializeRound", []byte{0x01})
	snap, err := srv.GetTxIntent(context.Background(), TxIntentRef{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Kind != "InitializeRound" {
		t.Fatalf("Kind = %s; want InitializeRound", snap.Kind)
	}
	if snap.Status != "confirmed" {
		t.Fatalf("Status = %s; want confirmed", snap.Status)
	}
}

func TestGetTxIntentNotFound(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	rs := newRoundInitSvc(t, rmAddr)
	stub := &stubSubmitter{err: errors.New("not in store")}
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs, Tx: stub})

	id := txintent.ComputeID("X", []byte{0x99})
	if _, err := srv.GetTxIntent(context.Background(), TxIntentRef{ID: id}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound; got %v", err)
	}
}

func TestGetTxIntentNoReader(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	rs := newRoundInitSvc(t, rmAddr)
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs})
	id := txintent.ComputeID("X", []byte{0x99})
	if _, err := srv.GetTxIntent(context.Background(), TxIntentRef{ID: id}); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("expected ErrUnimplemented; got %v", err)
	}
}

// stubRoundClock for the streaming test.
type stubRoundClockSrc struct {
	rounds chan chain.Round
	subErr error
}

func (s *stubRoundClockSrc) SubscribeRounds(_ context.Context) (<-chan chain.Round, error) {
	if s.subErr != nil {
		return nil, s.subErr
	}
	return s.rounds, nil
}

func TestStreamRoundEvents(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	rs := newRoundInitSvc(t, rmAddr)
	src := &stubRoundClockSrc{rounds: make(chan chain.Round, 2)}
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs, RC: src})

	src.rounds <- chain.Round{Number: 1}
	src.rounds <- chain.Round{Number: 2}
	close(src.rounds)

	var got []RoundEvent
	if err := srv.StreamRoundEvents(context.Background(), func(ev RoundEvent) error {
		got = append(got, ev)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events; want 2", len(got))
	}
	if got[0].Number != 1 || got[1].Number != 2 {
		t.Fatalf("events out of order: %+v", got)
	}
}

func TestStreamRoundEventsNoSource(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	rs := newRoundInitSvc(t, rmAddr)
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs})
	if err := srv.StreamRoundEvents(context.Background(), func(_ RoundEvent) error { return nil }); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("expected ErrUnimplemented; got %v", err)
	}
}

func TestStreamRoundEventsCtxCancel(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	rs := newRoundInitSvc(t, rmAddr)
	src := &stubRoundClockSrc{rounds: make(chan chain.Round)}
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs, RC: src})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if err := srv.StreamRoundEvents(ctx, func(_ RoundEvent) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected ctx cancel; got %v", err)
	}
}

func TestStreamRoundEventsSubscribeError(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	rs := newRoundInitSvc(t, rmAddr)
	src := &stubRoundClockSrc{subErr: errors.New("boom")}
	srv, _ := New(Config{Mode: types.ModeRoundInit, RoundInit: rs, RC: src})
	if err := srv.StreamRoundEvents(context.Background(), func(_ RoundEvent) error { return nil }); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetRoundStatusInBoth(t *testing.T) {
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	bmAddr := common.HexToAddress("0x000000000000000000000000000000000000FB01")
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	rs := newRoundInitSvc(t, rmAddr)
	rwd := newRewardSvc(t, bmAddr, orch, bondingmanager.TranscoderInfo{Active: true, ActivationRound: 1})
	srv, _ := New(Config{Mode: types.ModeBoth, RoundInit: rs, Reward: rwd})

	if _, err := srv.GetRoundStatus(context.Background(), struct{}{}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.GetRewardStatus(context.Background(), struct{}{}); err != nil {
		t.Fatal(err)
	}
}

func TestGetRewardStatusEmpty(t *testing.T) {
	bmAddr := common.HexToAddress("0x000000000000000000000000000000000000FB01")
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	rwd := newRewardSvc(t, bmAddr, orch, bondingmanager.TranscoderInfo{Active: true, ActivationRound: 1})
	srv, _ := New(Config{Mode: types.ModeReward, Reward: rwd})
	st, err := srv.GetRewardStatus(context.Background(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	// No rounds processed yet → eligibility is nil → out is mostly zero.
	if st.LastEarnedWei != nil && st.LastEarnedWei.Cmp(big.NewInt(0)) != 0 {
		t.Fatalf("LastEarnedWei = %v; want 0/nil", st.LastEarnedWei)
	}
}
