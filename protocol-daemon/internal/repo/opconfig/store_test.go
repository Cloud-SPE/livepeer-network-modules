package opconfig

import (
	"math/big"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/store"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/types"
	"github.com/ethereum/go-ethereum/common"
)

func TestNewReturnsDefaultsOnEmptyStore(t *testing.T) {
	s, err := New(store.Memory())
	if err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	want := types.DefaultOperationalConfig()
	if got.RewardEnabled != want.RewardEnabled || got.RoundInitEnabled != want.RoundInitEnabled {
		t.Errorf("fresh store should return defaults, got %+v", got)
	}
	if !got.RewardEnabled || got.RoundInitEnabled {
		t.Error("expected reward on, round-init off")
	}
}

func TestSetGetRoundTrip(t *testing.T) {
	s, err := New(store.Memory())
	if err != nil {
		t.Fatal(err)
	}
	addr := common.HexToAddress("0x000000000000000000000000000000000000dEaD")
	cfg := types.DefaultOperationalConfig()
	cfg.RoundInitEnabled = true
	cfg.TransferBond.Enabled = true
	cfg.TransferBond.Receiver = addr
	cfg.TransferBond.MinRetainWei = big.NewInt(1_000_000_000_000_000_000)

	stored, err := s.Set(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.RoundInitEnabled || !stored.TransferBond.Enabled {
		t.Error("stored config lost flags")
	}
	if stored.TransferBond.Receiver != addr {
		t.Error("receiver not persisted")
	}
	if stored.TransferBond.MinRetainWei.Cmp(big.NewInt(1_000_000_000_000_000_000)) != 0 {
		t.Error("retain wei not persisted")
	}

	got := s.Get()
	if got.TransferBond.Receiver != addr {
		t.Error("cached get lost receiver")
	}
}

func TestSetRejectsInvalid(t *testing.T) {
	s, err := New(store.Memory())
	if err != nil {
		t.Fatal(err)
	}
	cfg := types.DefaultOperationalConfig()
	cfg.WithdrawFees.Enabled = true // no receiver
	if _, err := s.Set(cfg); err == nil {
		t.Fatal("expected validation error enabling withdraw without receiver")
	}
}

func TestPersistsAcrossReopen(t *testing.T) {
	mem := store.Memory()
	s1, err := New(mem)
	if err != nil {
		t.Fatal(err)
	}
	cfg := types.DefaultOperationalConfig()
	cfg.RewardEnabled = false
	cfg.RoundInitEnabled = true
	if _, err := s1.Set(cfg); err != nil {
		t.Fatal(err)
	}

	// Re-open over the same backing store: cache rebuilt from disk.
	s2, err := New(mem)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.Get()
	if got.RewardEnabled {
		t.Error("reward should have persisted as disabled")
	}
	if !got.RoundInitEnabled {
		t.Error("round-init should have persisted as enabled")
	}
}

func TestGetReturnsIsolatedCopy(t *testing.T) {
	s, err := New(store.Memory())
	if err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	got.TransferBond.MinRetainWei.SetInt64(999) // mutate caller copy
	if s.Get().TransferBond.MinRetainWei.Sign() != 0 {
		t.Error("mutating returned config leaked into stored state")
	}
}
