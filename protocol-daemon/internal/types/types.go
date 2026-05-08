// Package types holds the pure-data domain values for protocol-daemon.
//
// No I/O imports. Other packages depend on this; this depends on nothing
// except chain-commons/chain (typed primitives).
package types

import (
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
)

// Mode is the daemon's operating mode. One binary, three modes.
type Mode string

const (
	// ModeRoundInit runs only the round-init service. Mode-specific RPCs
	// for reward (ForceRewardCall, GetRewardStatus) return Unimplemented.
	ModeRoundInit Mode = "round-init"

	// ModeReward runs only the reward service. Mode-specific RPCs for
	// round-init (ForceInitializeRound, GetRoundStatus) return Unimplemented.
	ModeReward Mode = "reward"

	// ModeBoth runs both services. All RPCs are available.
	ModeBoth Mode = "both"
)

// Validate reports whether the mode is one of the three valid values.
func (m Mode) Validate() error {
	switch m {
	case ModeRoundInit, ModeReward, ModeBoth:
		return nil
	default:
		return fmt.Errorf("invalid mode %q (must be round-init|reward|both)", m)
	}
}

// HasRoundInit reports whether the mode runs the round-init service.
func (m Mode) HasRoundInit() bool { return m == ModeRoundInit || m == ModeBoth }

// HasReward reports whether the mode runs the reward service.
func (m Mode) HasReward() bool { return m == ModeReward || m == ModeBoth }

// String returns the canonical name (used for log fields and metric labels).
func (m Mode) String() string { return string(m) }

// RewardEligibility captures the per-round eligibility decision for reward
// calling. Returned by the reward service for status RPCs and metrics.
type RewardEligibility struct {
	// OrchestratorAddress is the address being checked.
	OrchestratorAddress chain.Address

	// Round is the round whose eligibility is being assessed.
	Round chain.RoundNumber

	// Active reports whether the transcoder is currently active.
	Active bool

	// LastRewardRound is the most recent round in which the transcoder
	// received a reward call.
	LastRewardRound chain.RoundNumber

	// Eligible reports whether the transcoder is eligible to receive a
	// reward this round (Active && LastRewardRound < Round).
	Eligible bool

	// Reason is a short human-readable string explaining the decision.
	Reason string
}

// PoolHints holds the (prev, next) addresses BondingManager.rewardWithHint
// requires. Computed by walking the transcoder pool linked list.
//
// Both fields are zero-valued for the first or last transcoder in the pool;
// rewardWithHint accepts the zero address as a sentinel for "no neighbour".
type PoolHints struct {
	Prev chain.Address
	Next chain.Address
}

// IsZero reports whether neither hint is set.
func (h PoolHints) IsZero() bool {
	return h.Prev == (chain.Address{}) && h.Next == (chain.Address{})
}

// TranscoderInfo lives in chain-commons/providers/bondingmanager (since
// plan 0009 §A) — re-exported by protocol-daemon's bondingmanager
// package for callers that prefer the local import path.

// Error codes — stable strings used in structured logs and metric labels.
// Keep alphabetical and grouped by subsystem.
const (
	// Round-init.
	ErrCodeRoundInitAlreadyInitialized = "roundinit.already_initialized"
	ErrCodeRoundInitSubmitFailed       = "roundinit.submit_failed"
	ErrCodeRoundInitJitterCancelled    = "roundinit.jitter_cancelled"

	// Reward.
	ErrCodeRewardIneligible     = "reward.ineligible"
	ErrCodeRewardSubmitFailed   = "reward.submit_failed"
	ErrCodeRewardEventDecode    = "reward.event_decode_failed"
	ErrCodeRewardPoolWalkFailed = "reward.pool_walk_failed"

	// Preflight.
	ErrCodePreflightChainID         = "preflight.chain_id_mismatch"
	ErrCodePreflightControllerEmpty = "preflight.controller_addr_empty"
	ErrCodePreflightContractCode    = "preflight.contract_code_missing"
	ErrCodePreflightKeystore        = "preflight.keystore_decrypt_failed"
	ErrCodePreflightKeystoreAddr    = "preflight.keystore_addr_mismatch"
	ErrCodePreflightBalance         = "preflight.balance_below_min"
	ErrCodePreflightGasOracle       = "preflight.gas_oracle_failed"
)
