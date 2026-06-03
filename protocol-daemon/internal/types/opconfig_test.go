package types

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestDefaultOperationalConfig(t *testing.T) {
	c := DefaultOperationalConfig()
	if c.RoundInitEnabled {
		t.Error("round-init should default disabled")
	}
	if !c.RewardEnabled {
		t.Error("reward should default enabled")
	}
	if c.TransferBond.Enabled || c.WithdrawFees.Enabled {
		t.Error("transfer/withdraw should default disabled")
	}
	if !c.RewardBeforeTransfer {
		t.Error("reward-before-transfer should default on")
	}
	if c.TransferBond.MinRetainWei == nil || c.WithdrawFees.ThresholdWei == nil {
		t.Error("wei fields should be non-nil after default")
	}
	if err := c.Validate(); err != nil {
		t.Errorf("default config should validate: %v", err)
	}
}

func TestOperationalConfigValidate(t *testing.T) {
	addr := common.HexToAddress("0x000000000000000000000000000000000000dEaD")

	t.Run("transfer enabled without receiver rejected", func(t *testing.T) {
		c := DefaultOperationalConfig()
		c.TransferBond.Enabled = true
		if err := c.Validate(); err == nil {
			t.Fatal("expected error enabling transfer without receiver")
		}
	})

	t.Run("transfer enabled with receiver ok", func(t *testing.T) {
		c := DefaultOperationalConfig()
		c.TransferBond.Enabled = true
		c.TransferBond.Receiver = addr
		if err := c.Validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("withdraw enabled without receiver rejected", func(t *testing.T) {
		c := DefaultOperationalConfig()
		c.WithdrawFees.Enabled = true
		if err := c.Validate(); err == nil {
			t.Fatal("expected error enabling withdraw without receiver")
		}
	})

	t.Run("negative retain rejected", func(t *testing.T) {
		c := DefaultOperationalConfig()
		c.TransferBond.Enabled = true
		c.TransferBond.Receiver = addr
		c.TransferBond.MinRetainWei = big.NewInt(-1)
		if err := c.Validate(); err == nil {
			t.Fatal("expected error on negative retain")
		}
	})
}

func TestNormalize(t *testing.T) {
	c := OperationalConfig{}
	c.Normalize()
	if c.TransferBond.MinRetainWei == nil || c.WithdrawFees.ThresholdWei == nil {
		t.Fatal("Normalize should fill nil wei fields")
	}
}
