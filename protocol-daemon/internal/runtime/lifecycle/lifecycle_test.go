package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/timesource"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/roundclock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/txintent"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/providers/bondingmanager"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/repo/poolhints"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/reward"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/roundinit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/types"
)

// stubRM mirrors roundinit.RoundsManager.
type stubRM struct{ addr chain.Address }

func (s *stubRM) Address() chain.Address                                  { return s.addr }
func (s *stubRM) CurrentRoundInitialized(_ context.Context) (bool, error) { return true, nil }
func (s *stubRM) PackInitializeRound() ([]byte, error)                    { return []byte{1}, nil }

// stubBM mirrors reward.BondingManager.
type stubBM struct{ addr chain.Address }

func (s *stubBM) Address() chain.Address { return s.addr }
func (s *stubBM) GetTranscoder(_ context.Context, _ chain.Address) (bondingmanager.TranscoderInfo, error) {
	return bondingmanager.TranscoderInfo{Active: false}, nil
}
func (s *stubBM) GetFirstTranscoderInPool(_ context.Context) (chain.Address, error) {
	return chain.Address{}, nil
}
func (s *stubBM) GetNextTranscoderInPool(_ context.Context, _ chain.Address) (chain.Address, error) {
	return chain.Address{}, nil
}
func (s *stubBM) PackRewardWithHint(_, _ chain.Address) ([]byte, error) { return []byte{}, nil }

// noopSub satisfies roundinit.TxSubmitter and reward.TxSubmitter.
type noopSub struct{}

func (noopSub) Submit(_ context.Context, p txintent.Params) (txintent.IntentID, error) {
	return txintent.ComputeID(p.Kind, p.KeyParams), nil
}
func (noopSub) Status(_ context.Context, id txintent.IntentID) (txintent.TxIntent, error) {
	return txintent.TxIntent{ID: id}, nil
}
func (noopSub) Wait(_ context.Context, id txintent.IntentID) (txintent.TxIntent, error) {
	return txintent.TxIntent{ID: id, Status: txintent.StatusConfirmed}, nil
}

// fakeTimeSource is a no-op timesource for roundclock.New().
type fakeTimeSource struct{}

func (fakeTimeSource) CurrentRound(_ context.Context) (chain.Round, error) {
	return chain.Round{}, nil
}
func (fakeTimeSource) CurrentL1Block(_ context.Context) (chain.BlockNumber, error) {
	return 0, nil
}
func (fakeTimeSource) SubscribeRounds(_ context.Context) (<-chan chain.Round, error) {
	return make(chan chain.Round), nil
}
func (fakeTimeSource) SubscribeL1Blocks(_ context.Context) (<-chan chain.BlockNumber, error) {
	return make(chan chain.BlockNumber), nil
}

var _ timesource.TimeSource = fakeTimeSource{}

func newRoundInit(t *testing.T) *roundinit.Service {
	t.Helper()
	svc, err := roundinit.New(roundinit.Config{
		RoundsManager: &stubRM{addr: common.HexToAddress("0x000000000000000000000000000000000000FA01")},
		TxIntent:      noopSub{},
		GasLimit:      1_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func newReward(t *testing.T) *reward.Service {
	t.Helper()
	cache, err := poolhints.New(store.Memory())
	if err != nil {
		t.Fatal(err)
	}
	svc, err := reward.New(reward.Config{
		BondingManager: &stubBM{addr: common.HexToAddress("0x000000000000000000000000000000000000FB01")},
		TxIntent:       noopSub{},
		Cache:          cache,
		OrchAddress:    common.HexToAddress("0x00000000000000000000000000000000000000A1"),
		GasLimit:       1_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func newRoundClock(t *testing.T) roundclock.Clock {
	t.Helper()
	rc, err := roundclock.New(roundclock.Options{TimeSource: fakeTimeSource{}})
	if err != nil {
		t.Fatal(err)
	}
	return rc
}

func TestRunStartsAndStops(t *testing.T) {
	rc := newRoundClock(t)
	rs := newRoundInit(t)
	rwd := newReward(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(40 * time.Millisecond)
		cancel()
	}()
	if err := Run(ctx, Config{
		Mode: types.ModeBoth, RoundInit: rs, Reward: rwd, RoundClock: rc,
	}); err != nil {
		t.Fatalf("Run err = %v", err)
	}
}

func TestRunValidatesMode(t *testing.T) {
	if err := Run(context.Background(), Config{Mode: "x"}); err == nil {
		t.Fatal("expected mode-validate error")
	}
}

func TestRunRequiresRoundClock(t *testing.T) {
	if err := Run(context.Background(), Config{Mode: types.ModeBoth}); err == nil {
		t.Fatal("expected RoundClock error")
	}
}

func TestRunRequiresRoundInitInMode(t *testing.T) {
	if err := Run(context.Background(), Config{Mode: types.ModeRoundInit, RoundClock: newRoundClock(t)}); err == nil {
		t.Fatal("expected service error")
	}
}

func TestRunRequiresRewardInMode(t *testing.T) {
	if err := Run(context.Background(), Config{Mode: types.ModeReward, RoundClock: newRoundClock(t)}); err == nil {
		t.Fatal("expected service error")
	}
}

func TestRunRoundInitOnly(t *testing.T) {
	rc := newRoundClock(t)
	rs := newRoundInit(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(40 * time.Millisecond); cancel() }()
	if err := Run(ctx, Config{Mode: types.ModeRoundInit, RoundInit: rs, RoundClock: rc}); err != nil {
		t.Fatal(err)
	}
}

func TestRunRewardOnly(t *testing.T) {
	rc := newRoundClock(t)
	rwd := newReward(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(40 * time.Millisecond); cancel() }()
	if err := Run(ctx, Config{Mode: types.ModeReward, Reward: rwd, RoundClock: rc}); err != nil {
		t.Fatal(err)
	}
}
