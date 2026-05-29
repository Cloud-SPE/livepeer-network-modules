package types

import (
	"errors"
	"math/big"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
)

// OperationalConfig is the daemon's runtime-editable operational policy.
// It is owned by the daemon, persisted in BoltDB, and edited from the
// secure-orch console (never via CLI flags). Infra knobs (eth URLs,
// keystore, socket, chain/controller addresses) remain start-time config;
// this struct is only the policy that an operator changes day-to-day.
//
// Pure data — no I/O. The persistence layer (internal/repo/opconfig) owns
// serialization; the editing RPC owns the decimal↔wei conversion of
// operator input into the wei fields here.
type OperationalConfig struct {
	// RoundInitEnabled gates the automatic initializeRound submit. When
	// false the round-init service still observes round state (so
	// GetRoundStatus stays live) but does not submit. ForceInitializeRound
	// always works regardless. Default false.
	RoundInitEnabled bool

	// RewardEnabled gates the automatic per-round reward call. Default true.
	RewardEnabled bool

	// TransferBond configures the round-locked automatic bond transfer.
	TransferBond TransferBondConfig

	// WithdrawFees configures the round-locked automatic fee withdrawal.
	WithdrawFees WithdrawFeesConfig

	// RewardBeforeTransfer, when true, skips an auto transferBond for a
	// round unless this round's reward() has confirmed on-chain
	// (lastRewardRound == currentRound), so freshly-claimed rewards are
	// included in the transferred excess. Only meaningful when both reward
	// and transfer-bond are enabled. Default true.
	RewardBeforeTransfer bool
}

// TransferBondConfig configures automatic excess-bond transfer.
type TransferBondConfig struct {
	// Enabled turns on the round-locked auto transfer. Cannot be enabled
	// without a non-zero Receiver. Default false.
	Enabled bool

	// Receiver is the delegator address that receives transferred bond.
	Receiver chain.Address

	// MinRetainWei is the bonded stake (in wei) to retain on the
	// orchestrator; only the excess of pendingStake over this is moved.
	MinRetainWei chain.Wei
}

// WithdrawFeesConfig configures automatic ETH-fee withdrawal.
type WithdrawFeesConfig struct {
	// Enabled turns on the round-locked auto withdrawal. Cannot be enabled
	// without a non-zero Receiver. Default false.
	Enabled bool

	// Receiver is the address that receives withdrawn ETH fees.
	Receiver chain.Address

	// ThresholdWei is the minimum pendingFees (in wei) required before a
	// withdrawal fires.
	ThresholdWei chain.Wei
}

// DefaultOperationalConfig returns the first-boot defaults: reward on,
// everything else off. Zero-valued wei fields are normalized to 0.
func DefaultOperationalConfig() OperationalConfig {
	return OperationalConfig{
		RoundInitEnabled:     false,
		RewardEnabled:        true,
		RewardBeforeTransfer: true,
		TransferBond:         TransferBondConfig{MinRetainWei: new(big.Int)},
		WithdrawFees:         WithdrawFeesConfig{ThresholdWei: new(big.Int)},
	}
}

// Normalize fills nil wei pointers with zero so callers never deref nil.
func (c *OperationalConfig) Normalize() {
	if c.TransferBond.MinRetainWei == nil {
		c.TransferBond.MinRetainWei = new(big.Int)
	}
	if c.WithdrawFees.ThresholdWei == nil {
		c.WithdrawFees.ThresholdWei = new(big.Int)
	}
}

// Validate enforces the invariants that protect autonomous fund movement.
// Returns the first violation.
func (c *OperationalConfig) Validate() error {
	if c.TransferBond.Enabled {
		if c.TransferBond.Receiver == (chain.Address{}) {
			return errors.New("opconfig: transfer-bond cannot be enabled without a receiver address")
		}
		if c.TransferBond.MinRetainWei != nil && c.TransferBond.MinRetainWei.Sign() < 0 {
			return errors.New("opconfig: transfer-bond min-retain must be non-negative")
		}
	}
	if c.WithdrawFees.Enabled {
		if c.WithdrawFees.Receiver == (chain.Address{}) {
			return errors.New("opconfig: withdraw-fees cannot be enabled without a receiver address")
		}
		if c.WithdrawFees.ThresholdWei != nil && c.WithdrawFees.ThresholdWei.Sign() < 0 {
			return errors.New("opconfig: withdraw-fees threshold must be non-negative")
		}
	}
	return nil
}
