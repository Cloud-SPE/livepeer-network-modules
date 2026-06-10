// Package bondingadmin implements the orchestrator self-service actions
// that write to BondingManager on behalf of the daemon's wallet (which is
// the orchestrator address): set reward/fee cut, transfer bonded LPT, and
// withdraw ETH fees.
//
// Set-shares is a one-shot operator action gated only on the round being
// initialized. Transfer-bond and withdraw-fees are round-LOCKED automated
// actions (modeled on the livepeer-funds-transfer reference) that also
// expose Force entry points so an operator can trigger the same once-per-
// round handler immediately.
//
// Every write routes through submitGuarded, which (1) reads the fresh
// authoritative round-lock state, (2) dry-runs the exact calldata via
// eth_call from the orchestrator address to catch reverts without spending
// gas, then (3) submits through chain-commons.services.txintent. Durable
// idempotency keys make each automated action fire at most once per round
// even across restarts.
package bondingadmin

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/txintent"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/providers/bondingmanager"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/types"
)

// BondingManager is the subset of the provider binding this service uses.
type BondingManager interface {
	Address() chain.Address
	PackTranscoder(rewardCutPPM, feeSharePPM uint64) ([]byte, error)
	PackTransferBond(recipient chain.Address, amount *big.Int, oldPrev, oldNext, newPrev, newNext chain.Address) ([]byte, error)
	PackWithdrawFees(recipient chain.Address, amount *big.Int) ([]byte, error)
	PendingStake(ctx context.Context, addr chain.Address, endRound chain.RoundNumber) (*big.Int, error)
	PendingFees(ctx context.Context, addr chain.Address, endRound chain.RoundNumber) (*big.Int, error)
	GetTranscoder(ctx context.Context, addr chain.Address) (bondingmanager.TranscoderInfo, error)
	TransferBondHints(ctx context.Context, orch chain.Address, amount *big.Int) (oldHints, newHints bondingmanager.TranscoderPoolHints, err error)
}

// gasEstimator is the optional capability submitGuarded uses to size the gas
// limit from a simulation instead of a static config value. The production
// Caller (the multi-RPC client) implements it; test fakes that don't simply
// fall back to the configured GasLimit.
type gasEstimator interface {
	EstimateGas(ctx context.Context, call ethereum.CallMsg) (uint64, error)
}

// RoundsManager is the subset of the rounds provider this service reads for
// the round-lock gate.
type RoundsManager interface {
	CurrentRoundInitialized(ctx context.Context) (bool, error)
	CurrentRoundLocked(ctx context.Context) (bool, error)
}

// TxSubmitter is the subset of txintent.Manager used here.
type TxSubmitter interface {
	Submit(ctx context.Context, p txintent.Params) (txintent.IntentID, error)
}

// ConfigSource supplies the current operational config (the daemon's
// persisted policy). Read fresh on every action so mid-flight edits apply.
type ConfigSource interface {
	Get() types.OperationalConfig
}

// Caller issues read-only eth_calls for the pre-submit dry-run.
type Caller interface {
	CallContract(ctx context.Context, call ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
}

// SkipCode is a stable machine identifier for why an action did not submit.
type SkipCode uint32

// Skip codes. Numeric values mirror protocolv1.SkipReason_Code (10-14) so
// the gRPC convert layer is a one-line cast; keep them in sync.
const (
	SkipCodeUnspecified       SkipCode = 0
	SkipCodeRoundNotLocked    SkipCode = 10
	SkipCodeNothingToTransfer SkipCode = 11
	SkipCodeBelowFeeThreshold SkipCode = 12
	SkipCodeRewardNotCalled   SkipCode = 13
	SkipCodeDisabled          SkipCode = 14
)

// SkipReason carries why an action did not submit a tx.
type SkipReason struct {
	Reason string
	Code   SkipCode
}

// ActionResult is the outcome of an action. Exactly one of IntentID or
// Skip is set on success.
type ActionResult struct {
	IntentID txintent.IntentID
	Skip     *SkipReason
}

// Config wires the service.
type Config struct {
	BondingManager BondingManager
	RoundsManager  RoundsManager
	TxIntent       TxSubmitter
	Caller         Caller
	Config         ConfigSource
	OrchAddress    chain.Address
	GasLimit       uint64
	Logger         logger.Logger
}

// Service performs the bonding-admin actions.
type Service struct {
	cfg Config
}

// New constructs a Service, validating required dependencies.
func New(cfg Config) (*Service, error) {
	if cfg.BondingManager == nil {
		return nil, errors.New("bondingadmin: BondingManager is required")
	}
	if cfg.RoundsManager == nil {
		return nil, errors.New("bondingadmin: RoundsManager is required")
	}
	if cfg.TxIntent == nil {
		return nil, errors.New("bondingadmin: TxIntent is required")
	}
	if cfg.Caller == nil {
		return nil, errors.New("bondingadmin: Caller is required")
	}
	if cfg.Config == nil {
		return nil, errors.New("bondingadmin: Config is required")
	}
	if cfg.OrchAddress == (chain.Address{}) {
		return nil, errors.New("bondingadmin: OrchAddress is required")
	}
	if cfg.GasLimit == 0 {
		return nil, errors.New("bondingadmin: GasLimit is required (>0)")
	}
	return &Service{cfg: cfg}, nil
}

// SetTranscoder submits a transcoder(rewardCut, feeShare) tx. rewardCutPPM
// and feeSharePPM are already in parts-per-million (the RPC layer converts
// the operator's percentages, including the fee-cut→fee-share flip).
// Idempotency keys on the (rewardCut, feeShare) values so a duplicate
// submit of identical values while one is in-flight is a no-op. Gated on
// the round being initialized.
func (s *Service) SetTranscoder(ctx context.Context, rewardCutPPM, feeSharePPM uint64) (ActionResult, error) {
	if rewardCutPPM > types.PPMDenominator || feeSharePPM > types.PPMDenominator {
		return ActionResult{}, fmt.Errorf("bondingadmin: cut/share out of range (ppm > %d)", types.PPMDenominator)
	}
	initialized, err := s.cfg.RoundsManager.CurrentRoundInitialized(ctx)
	if err != nil {
		return ActionResult{}, fmt.Errorf("currentRoundInitialized: %w", err)
	}
	if !initialized {
		return ActionResult{Skip: &SkipReason{Reason: "round not initialized", Code: SkipCodeRoundNotLocked}}, nil
	}
	calldata, err := s.cfg.BondingManager.PackTranscoder(rewardCutPPM, feeSharePPM)
	if err != nil {
		return ActionResult{}, err
	}
	key := make([]byte, 16)
	big.NewInt(0).SetUint64(rewardCutPPM).FillBytes(key[0:8])
	big.NewInt(0).SetUint64(feeSharePPM).FillBytes(key[8:16])
	return s.submitGuarded(ctx, "SetTranscoder", key, calldata,
		map[string]string{"reward_cut_ppm": fmt.Sprint(rewardCutPPM), "fee_share_ppm": fmt.Sprint(feeSharePPM)})
}

// TransferBond runs the once-per-round excess-bond transfer for `round`.
// Used by both the round-locked automation and ForceTransferBond. Requires
// the round to be locked+initialized, transfer-bond enabled with a
// receiver, and pendingStake above the configured retain. Optional reward-
// before-transfer guard requires this round's reward to have confirmed.
func (s *Service) TransferBond(ctx context.Context, round chain.Round) (ActionResult, error) {
	cfg := s.cfg.Config.Get()
	if !cfg.TransferBond.Enabled {
		return ActionResult{Skip: &SkipReason{Reason: "transfer-bond disabled", Code: SkipCodeDisabled}}, nil
	}
	if skip, err := s.requireLocked(ctx); err != nil || skip != nil {
		return ActionResult{Skip: skip}, err
	}
	if cfg.RewardBeforeTransfer && cfg.RewardEnabled {
		tinfo, err := s.cfg.BondingManager.GetTranscoder(ctx, s.cfg.OrchAddress)
		if err != nil {
			return ActionResult{}, fmt.Errorf("getTranscoder: %w", err)
		}
		if tinfo.LastRewardRound < round.Number {
			return ActionResult{Skip: &SkipReason{
				Reason: "this round's reward not yet confirmed",
				Code:   SkipCodeRewardNotCalled,
			}}, nil
		}
	}
	pendingStake, err := s.cfg.BondingManager.PendingStake(ctx, s.cfg.OrchAddress, round.Number)
	if err != nil {
		return ActionResult{}, fmt.Errorf("pendingStake: %w", err)
	}
	transferable := new(big.Int).Sub(pendingStake, cfg.TransferBond.MinRetainWei)
	if transferable.Sign() <= 0 {
		return ActionResult{Skip: &SkipReason{
			Reason: "pending stake at or below retain",
			Code:   SkipCodeNothingToTransfer,
		}}, nil
	}
	// Compute SortedDoublyLL position hints so the on-chain pool reposition is
	// O(1) instead of a full active-set scan. Hints are an optimization, not a
	// correctness requirement: on failure we fall back to zero hints and let
	// gas estimation size the (more expensive) full-scan transfer.
	oldHints, newHints, err := s.cfg.BondingManager.TransferBondHints(ctx, s.cfg.OrchAddress, transferable)
	if err != nil {
		if s.cfg.Logger != nil {
			s.cfg.Logger.Warn("transferBond hint computation failed; using zero hints",
				logger.String("err_code", types.ErrCodeBondingAdminHintFailed),
				logger.Err(err))
		}
		oldHints, newHints = bondingmanager.TranscoderPoolHints{}, bondingmanager.TranscoderPoolHints{}
	}
	calldata, err := s.cfg.BondingManager.PackTransferBond(
		cfg.TransferBond.Receiver, transferable,
		oldHints.PosPrev, oldHints.PosNext, newHints.PosPrev, newHints.PosNext)
	if err != nil {
		return ActionResult{}, err
	}
	return s.submitGuarded(ctx, "TransferBond", round.Number.Bytes(), calldata, map[string]string{
		"round":    fmt.Sprint(round.Number),
		"receiver": cfg.TransferBond.Receiver.Hex(),
		"amount":   transferable.String(),
	})
}

// WithdrawFees runs the once-per-round fee withdrawal for `round`. Used by
// both the automation and ForceWithdrawFees. Requires the round to be
// locked+initialized, withdraw-fees enabled with a receiver, and
// pendingFees at or above the configured threshold.
func (s *Service) WithdrawFees(ctx context.Context, round chain.Round) (ActionResult, error) {
	cfg := s.cfg.Config.Get()
	if !cfg.WithdrawFees.Enabled {
		return ActionResult{Skip: &SkipReason{Reason: "withdraw-fees disabled", Code: SkipCodeDisabled}}, nil
	}
	if skip, err := s.requireLocked(ctx); err != nil || skip != nil {
		return ActionResult{Skip: skip}, err
	}
	pendingFees, err := s.cfg.BondingManager.PendingFees(ctx, s.cfg.OrchAddress, round.Number)
	if err != nil {
		return ActionResult{}, fmt.Errorf("pendingFees: %w", err)
	}
	if pendingFees.Sign() <= 0 || pendingFees.Cmp(cfg.WithdrawFees.ThresholdWei) < 0 {
		return ActionResult{Skip: &SkipReason{
			Reason: "pending fees below threshold",
			Code:   SkipCodeBelowFeeThreshold,
		}}, nil
	}
	calldata, err := s.cfg.BondingManager.PackWithdrawFees(cfg.WithdrawFees.Receiver, pendingFees)
	if err != nil {
		return ActionResult{}, err
	}
	return s.submitGuarded(ctx, "WithdrawFees", round.Number.Bytes(), calldata, map[string]string{
		"round":    fmt.Sprint(round.Number),
		"receiver": cfg.WithdrawFees.Receiver.Hex(),
		"amount":   pendingFees.String(),
	})
}

// requireLocked returns a non-nil Skip when the round is not both locked
// and initialized. (locked is purely block-based and does not imply
// initialized, so we require both before a fund-movement submit.)
func (s *Service) requireLocked(ctx context.Context) (*SkipReason, error) {
	initialized, err := s.cfg.RoundsManager.CurrentRoundInitialized(ctx)
	if err != nil {
		return nil, fmt.Errorf("currentRoundInitialized: %w", err)
	}
	locked, err := s.cfg.RoundsManager.CurrentRoundLocked(ctx)
	if err != nil {
		return nil, fmt.Errorf("currentRoundLocked: %w", err)
	}
	if !locked || !initialized {
		return &SkipReason{Reason: "round not locked", Code: SkipCodeRoundNotLocked}, nil
	}
	return nil, nil
}

// submitGuarded dry-runs the calldata via eth_call from the orchestrator
// address (catching reverts without spending gas), then submits through
// txintent with the given idempotency key.
func (s *Service) submitGuarded(ctx context.Context, kind string, keyParams, calldata []byte, metadata map[string]string) (ActionResult, error) {
	to := s.cfg.BondingManager.Address()
	if _, err := s.cfg.Caller.CallContract(ctx, ethereum.CallMsg{
		From: s.cfg.OrchAddress,
		To:   &to,
		Data: calldata,
	}, nil); err != nil {
		if s.cfg.Logger != nil {
			s.cfg.Logger.Warn("bondingadmin dry-run reverted",
				logger.String("kind", kind),
				logger.String("err_code", types.ErrCodeBondingAdminDryRunFailed),
				logger.Err(err))
		}
		return ActionResult{}, fmt.Errorf("%s: %w", types.ErrCodeBondingAdminDryRunFailed, err)
	}
	gasLimit := s.gasLimitFor(ctx, kind, to, calldata)
	id, err := s.cfg.TxIntent.Submit(ctx, txintent.Params{
		Kind:      kind,
		KeyParams: keyParams,
		To:        to,
		CallData:  calldata,
		Value:     new(big.Int),
		GasLimit:  gasLimit,
		Metadata:  metadata,
	})
	if err != nil {
		return ActionResult{}, fmt.Errorf("%s: %w", types.ErrCodeBondingAdminSubmitFailed, err)
	}
	if s.cfg.Logger != nil {
		s.cfg.Logger.Info("bondingadmin submitted",
			logger.String("kind", kind),
			logger.String("intent_id", id.Hex()))
	}
	return ActionResult{IntentID: id}, nil
}

// gasLimitFor picks the gas limit for a submit. When the Caller can estimate
// gas it simulates the exact calldata and applies 50% headroom (covering
// SortedDoublyLL hint drift between estimate and mine); the configured
// GasLimit acts as a floor so estimation can only ever raise the ceiling,
// never lower it below the operator's configured value. If estimation is
// unavailable or fails, the configured GasLimit is used unchanged.
func (s *Service) gasLimitFor(ctx context.Context, kind string, to chain.Address, calldata []byte) uint64 {
	est, ok := s.cfg.Caller.(gasEstimator)
	if !ok {
		return s.cfg.GasLimit
	}
	used, err := est.EstimateGas(ctx, ethereum.CallMsg{
		From: s.cfg.OrchAddress,
		To:   &to,
		Data: calldata,
	})
	if err != nil {
		if s.cfg.Logger != nil {
			s.cfg.Logger.Warn("bondingadmin gas estimate failed; using configured GasLimit",
				logger.String("kind", kind),
				logger.Err(err))
		}
		return s.cfg.GasLimit
	}
	buffered := used + used/2
	if buffered > s.cfg.GasLimit {
		return buffered
	}
	return s.cfg.GasLimit
}
