// Package reward implements the reward-calling service.
//
// Subscribes to typed Round events from chain-commons.services.roundclock.
// For each round, checks BondingManager.GetTranscoder + isActiveTranscoder
// for the configured orchestrator. If eligible (Active && LastRewardRound
// < currentRound), walks the transcoder pool linked list to compute
// (prev, next) positional hints (with cache hits short-circuiting the
// walk), then submits a "RewardWithHint" TxIntent.
//
// Earnings are observed by parsing the BondingManager.Reward event from
// the receipt logs after TxIntent.Wait returns.
//
// Pattern recorded in docs/exec-plans/completed/0020-protocol-daemon-migration.md §7.
package reward

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum"
	ethtypes "github.com/ethereum/go-ethereum/core/types"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/roundclock"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/txintent"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/providers/bondingmanager"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/types"
)

// BondingManager is the subset of the ABI binding the service depends on.
type BondingManager interface {
	Address() chain.Address
	GetTranscoder(ctx context.Context, addr chain.Address) (bondingmanager.TranscoderInfo, error)
	GetFirstTranscoderInPool(ctx context.Context) (chain.Address, error)
	GetNextTranscoderInPool(ctx context.Context, addr chain.Address) (chain.Address, error)
	PackRewardWithHint(prev, next chain.Address) ([]byte, error)
}

// TxSubmitter is the subset of chain-commons.txintent.Manager the service
// uses.
type TxSubmitter interface {
	Submit(ctx context.Context, p txintent.Params) (txintent.IntentID, error)
	Status(ctx context.Context, id txintent.IntentID) (txintent.TxIntent, error)
	Wait(ctx context.Context, id txintent.IntentID) (txintent.TxIntent, error)
	// Resubmit re-drives a terminally-failed intent with fresh calldata,
	// broadcasting a new tx. Used by the force path to override the per-round
	// idempotency guard when an earlier attempt reverted.
	Resubmit(ctx context.Context, id txintent.IntentID, calldata []byte) error
}

// RewardCaller issues the read-only chain calls the force path uses to decide
// whether a forced reward is worth broadcasting: an eth_call dry-run of
// reward() (catching reverts without spending gas) and a balance/gas-price
// read (catching a wallet that can't afford the tx). The multi-RPC client
// satisfies it; it is optional — when nil, the force path skips these
// pre-send checks and submits directly.
type RewardCaller interface {
	CallContract(ctx context.Context, call ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
	BalanceAt(ctx context.Context, addr chain.Address, blockNumber *big.Int) (*big.Int, error)
	SuggestGasPrice(ctx context.Context) (*big.Int, error)
}

// PoolHintsCache is the cache shape the service depends on. Returns
// hits with ok=true.
type PoolHintsCache interface {
	Get(round chain.RoundNumber, orchAddr chain.Address) (types.PoolHints, bool, error)
	Put(round chain.RoundNumber, orchAddr chain.Address, hints types.PoolHints) error
	PurgeBefore(cutoff chain.RoundNumber) (int, error)
}

// Config holds Service dependencies.
type Config struct {
	BondingManager BondingManager
	TxIntent       TxSubmitter
	Cache          PoolHintsCache
	Clock          clock.Clock

	// Caller backs the force path's pre-send checks (eth_call dry-run +
	// balance). Optional: when nil those checks are skipped. The automatic
	// per-round path never uses it.
	Caller RewardCaller

	OrchAddress chain.Address
	GasLimit    uint64

	// Enabled reports whether the automatic per-round reward call is on.
	// Read fresh each round so a persisted-config toggle takes effect on
	// the next round. When nil, automatic reward is always on (preserves
	// pre-config behavior). When it returns false the Run loop runs
	// observe-only: it refreshes eligibility into Status but does not
	// submit. The operator Force path (TryReward) ignores this.
	Enabled func() bool

	Logger  logger.Logger
	Metrics metrics.Recorder

	// PurgeWindow is how many old rounds we keep in the cache. Rounds
	// older than (currentRound - PurgeWindow) are evicted on each new
	// round. Default 5.
	PurgeWindow chain.RoundNumber
}

// SkipCode is a stable machine identifier for why a force-action did
// not fire a transaction. Numeric values mirror
// protocolv1.SkipReason_Code so the gRPC convert layer is a one-line
// cast; keep the two in sync if proto values change.
type SkipCode uint32

const (
	// SkipCodeUnspecified is the zero value (not used in current code
	// paths; reserved so a future skip path the client doesn't
	// recognize maps here).
	SkipCodeUnspecified SkipCode = 0

	// SkipCodeAlreadyRewarded indicates tinfo.LastRewardRound >= round.
	SkipCodeAlreadyRewarded SkipCode = 1

	// SkipCodeTranscoderInactive indicates !tinfo.IsActiveAtRound(round).
	SkipCodeTranscoderInactive SkipCode = 2

	// SkipCodeRoundNotInitialized indicates !round.Initialized. The
	// BondingManager reward()/rewardWithHint() functions carry the
	// currentRoundInitialized modifier and revert ("current round is not
	// initialized") until initializeRound has landed for the current round,
	// so we wait rather than submit a tx that is guaranteed to revert.
	// Mirrors protocolv1.SkipReason_CODE_ROUND_NOT_INITIALIZED (=4).
	SkipCodeRoundNotInitialized SkipCode = 4

	// SkipCodeRewardInFlight indicates a reward tx for this round is already
	// pending/unconfirmed — forcing another would double-broadcast and the
	// second tx would revert as "already rewarded". Force-path only.
	// Mirrors protocolv1.SkipReason_CODE_REWARD_IN_FLIGHT (=5).
	SkipCodeRewardInFlight SkipCode = 5

	// SkipCodeRewardWouldRevert indicates the eth_call dry-run of reward()
	// reverted; the human reason carries the contract's revert string. Force
	// declined to spend gas on a guaranteed-revert tx. Force-path only.
	// Mirrors protocolv1.SkipReason_CODE_REWARD_WOULD_REVERT (=6).
	SkipCodeRewardWouldRevert SkipCode = 6

	// SkipCodeInsufficientBalance indicates the wallet balance is below the
	// gas cost of the tx, so broadcasting would fail. Force-path only.
	// Mirrors protocolv1.SkipReason_CODE_INSUFFICIENT_BALANCE (=7).
	SkipCodeInsufficientBalance SkipCode = 7
)

// SkipReason carries why TryReward did not submit a tx.
type SkipReason struct {
	Reason string
	Code   SkipCode
}

// ForceResult is the outcome of an operator-triggered TryReward.
// Exactly one of IntentID or Skip is set on success: Skip != nil for a
// short-circuit (no tx fired); otherwise IntentID is the submitted tx.
// On error (returned alongside), ForceResult is the zero value.
type ForceResult struct {
	IntentID txintent.IntentID
	Skip     *SkipReason
}

// Service is the reward service.
type Service struct {
	cfg Config

	mu              sync.Mutex
	lastRound       chain.RoundNumber
	lastEligibility *types.RewardEligibility
	lastIntent      *txintent.IntentID
	lastEarnedWei   *big.Int
	lastErr         error
}

// New constructs a Service.
func New(cfg Config) (*Service, error) {
	if cfg.BondingManager == nil {
		return nil, errors.New("reward: BondingManager is required")
	}
	if cfg.TxIntent == nil {
		return nil, errors.New("reward: TxIntent is required")
	}
	if cfg.Cache == nil {
		return nil, errors.New("reward: Cache is required")
	}
	if cfg.OrchAddress == (chain.Address{}) {
		return nil, errors.New("reward: OrchAddress is required")
	}
	if cfg.GasLimit == 0 {
		return nil, errors.New("reward: GasLimit is required (>0)")
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.System()
	}
	if cfg.Metrics == nil {
		cfg.Metrics = metrics.NoOp()
	}
	if cfg.PurgeWindow == 0 {
		cfg.PurgeWindow = 5
	}
	return &Service{cfg: cfg}, nil
}

// Run subscribes to Round events and processes each.
func (s *Service) Run(ctx context.Context, rc roundclock.Clock) error {
	rounds, err := rc.SubscribeRounds(ctx)
	if err != nil {
		return fmt.Errorf("reward: subscribe rounds: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case r, ok := <-rounds:
			if !ok {
				return nil
			}
			if s.cfg.Enabled != nil && !s.cfg.Enabled() {
				if err := s.observe(ctx, r); err != nil {
					s.recordError(r, err)
					if s.cfg.Logger != nil {
						s.cfg.Logger.Warn("reward observe failed",
							logger.Uint64("round", uint64(r.Number)),
							logger.Err(err),
						)
					}
				}
				continue
			}
			if err := s.tryReward(ctx, r); err != nil {
				s.recordError(r, err)
				s.metricsCounter("livepeer_protocol_reward_total",
					metrics.Labels{"outcome": "error"}, 1)
				if s.cfg.Logger != nil {
					s.cfg.Logger.Warn("reward failed",
						logger.Uint64("round", uint64(r.Number)),
						logger.Err(err),
					)
				}
			}
		}
	}
}

// TryReward is the operator-callable entry point for ForceRewardCall. Unlike
// the automatic per-round path, it is an explicit override: when the orch is
// eligible it broadcasts a NEW reward transaction even if an earlier intent
// for this round failed — re-driving that intent past the (round, orch)
// idempotency guard. It declines to send (returning a typed Skip with a clear,
// operator-facing reason) only when a send would be pointless: already
// rewarded, another reward tx already in flight, the round not initialized,
// the wallet can't afford the gas, or an eth_call dry-run shows the tx would
// revert. Any actual broadcast spends gas — callers should surface that.
func (s *Service) TryReward(ctx context.Context, round chain.Round) (ForceResult, error) {
	return s.forceReward(ctx, round)
}

// observe refreshes eligibility into Status without submitting. Used by the
// Run loop when automatic reward is toggled off so GetRewardStatus stays
// live while the daemon does not call reward itself.
func (s *Service) observe(ctx context.Context, round chain.Round) error {
	tinfo, err := s.cfg.BondingManager.GetTranscoder(ctx, s.cfg.OrchAddress)
	if err != nil {
		return fmt.Errorf("getTranscoder: %w", err)
	}
	elig := types.RewardEligibility{
		OrchestratorAddress: s.cfg.OrchAddress,
		Round:               round.Number,
		Active:              tinfo.IsActiveAtRound(round.Number),
		LastRewardRound:     tinfo.LastRewardRound,
	}
	elig.Eligible = elig.Active && tinfo.LastRewardRound < round.Number
	if elig.Eligible {
		elig.Reason = "eligible (auto-reward disabled)"
	} else {
		elig.Reason = "auto-reward disabled"
	}
	s.recordSkip(round, elig)
	s.metricsCounter("livepeer_protocol_reward_total",
		metrics.Labels{"outcome": "observed"}, 1)
	return nil
}

// tryReward handles one round driven by the Run loop. Returns nil on
// skipped (ineligible) or successful submit; an error otherwise.
func (s *Service) tryReward(ctx context.Context, round chain.Round) error {
	_, err := s.autoReward(ctx, round)
	return err
}

// evalEligibility reads the orch's transcoder info and reports whether it can
// reward this round. skip is non-nil — with a clear, operator-facing reason
// and a stable code — when it cannot: not active, already rewarded, or the
// round not yet initialized (reward() reverts "current round is not
// initialized" until initializeRound lands; treating it as not-yet-eligible
// also avoids creating a terminal-failed intent that idempotency would pin for
// the whole round). The returned snapshot is always populated for Status.
func (s *Service) evalEligibility(ctx context.Context, round chain.Round) (types.RewardEligibility, *SkipReason, error) {
	tinfo, err := s.cfg.BondingManager.GetTranscoder(ctx, s.cfg.OrchAddress)
	if err != nil {
		return types.RewardEligibility{}, nil, fmt.Errorf("getTranscoder: %w", err)
	}
	elig := types.RewardEligibility{
		OrchestratorAddress: s.cfg.OrchAddress,
		Round:               round.Number,
		Active:              tinfo.IsActiveAtRound(round.Number),
		LastRewardRound:     tinfo.LastRewardRound,
	}
	elig.Eligible = elig.Active && tinfo.LastRewardRound < round.Number && round.Initialized
	if elig.Eligible {
		return elig, nil, nil
	}
	var skip SkipReason
	switch {
	case !elig.Active:
		skip = SkipReason{Reason: "orchestrator is not active this round — not eligible to reward", Code: SkipCodeTranscoderInactive}
	case tinfo.LastRewardRound >= round.Number:
		skip = SkipReason{Reason: fmt.Sprintf("reward already called for round %d — nothing to do", round.Number), Code: SkipCodeAlreadyRewarded}
	case !round.Initialized:
		skip = SkipReason{Reason: "current round is not initialized yet — wait for initializeRound (or force a round-init)", Code: SkipCodeRoundNotInitialized}
	default:
		skip = SkipReason{Reason: "ineligible", Code: SkipCodeUnspecified}
	}
	elig.Reason = skip.Reason
	return elig, &skip, nil
}

// autoReward is the automatic per-round path (Run loop). It respects the
// (round, orch) idempotency guard: it submits once and, on a repeat round
// event, surfaces a prior intent's real state rather than re-broadcasting.
func (s *Service) autoReward(ctx context.Context, round chain.Round) (ForceResult, error) {
	start := s.cfg.Clock.Now()
	defer func() {
		dur := s.cfg.Clock.Now().Sub(start).Seconds()
		s.metricsHistogram("livepeer_protocol_reward_duration_seconds", metrics.Labels{}, dur)
	}()

	// Purge old cache entries to keep the store bounded. Best-effort —
	// errors here don't stop the reward attempt.
	if round.Number > s.cfg.PurgeWindow {
		_, _ = s.cfg.Cache.PurgeBefore(round.Number - s.cfg.PurgeWindow)
	}

	elig, skip, err := s.evalEligibility(ctx, round)
	if err != nil {
		return ForceResult{}, err
	}
	if skip != nil {
		s.recordSkip(round, elig)
		s.metricsCounter("livepeer_protocol_reward_total", metrics.Labels{"outcome": "skipped"}, 1)
		s.metricsGauge("livepeer_protocol_eligible_round_count", metrics.Labels{}, 0)
		s.metricsGauge("livepeer_protocol_active_status", metrics.Labels{}, boolFloat(elig.Active))
		if s.cfg.Logger != nil {
			s.cfg.Logger.Debug("reward skipped",
				logger.Uint64("round", uint64(round.Number)),
				logger.String("reason", skip.Reason),
			)
		}
		return ForceResult{Skip: skip}, nil
	}

	hints, calldata, err := s.buildRewardCalldata(ctx, round.Number)
	if err != nil {
		return ForceResult{}, err
	}

	intentID, err := s.cfg.TxIntent.Submit(ctx, s.rewardParams(round.Number, calldata))
	if err != nil {
		return ForceResult{}, fmt.Errorf("%s: %w", types.ErrCodeRewardSubmitFailed, err)
	}

	s.recordSuccess(round, elig, intentID)
	s.metricsGauge("livepeer_protocol_eligible_round_count", metrics.Labels{}, 1)
	s.metricsGauge("livepeer_protocol_active_status", metrics.Labels{}, 1)

	// Submit is idempotent on (round, orch), so the returned intent may be a
	// pre-existing one — including one that already reverted. Inspect its real
	// status so we don't log "reward submitted" (and count a fresh submit) for
	// an intent that is actually stuck terminal: that false-success logging is
	// what previously masked on-chain reverts as repeating success lines.
	st, statusErr := s.cfg.TxIntent.Status(ctx, intentID)
	switch {
	case statusErr == nil && st.Status == txintent.StatusFailed:
		s.metricsCounter("livepeer_protocol_reward_total",
			metrics.Labels{"outcome": "already_failed"}, 1)
		if s.cfg.Logger != nil {
			s.cfg.Logger.Warn("reward intent already failed this round; not retried (use Force Reward to re-broadcast)",
				logger.Uint64("round", uint64(round.Number)),
				logger.String("intent_id", intentID.Hex()),
				logger.String("reason", failedReason(st)),
			)
		}
	case statusErr == nil && st.Status == txintent.StatusConfirmed:
		s.metricsCounter("livepeer_protocol_reward_total",
			metrics.Labels{"outcome": "already_confirmed"}, 1)
		if s.cfg.Logger != nil {
			s.cfg.Logger.Debug("reward already confirmed this round",
				logger.Uint64("round", uint64(round.Number)),
				logger.String("intent_id", intentID.Hex()),
			)
		}
	default:
		s.metricsCounter("livepeer_protocol_reward_total",
			metrics.Labels{"outcome": "submitted"}, 1)
		if s.cfg.Logger != nil {
			s.cfg.Logger.Info("reward submitted",
				logger.Uint64("round", uint64(round.Number)),
				logger.String("intent_id", intentID.Hex()),
				logger.String("prev", hints.Prev.Hex()),
				logger.String("next", hints.Next.Hex()),
			)
		}
	}

	return ForceResult{IntentID: intentID}, nil
}

// forceReward is the operator override path. It broadcasts a fresh reward tx
// whenever the chain would accept one — re-driving a prior terminal-failed
// intent past idempotency — and otherwise returns a typed Skip explaining,
// in plain language, why no transaction was sent.
func (s *Service) forceReward(ctx context.Context, round chain.Round) (ForceResult, error) {
	start := s.cfg.Clock.Now()
	defer func() {
		dur := s.cfg.Clock.Now().Sub(start).Seconds()
		s.metricsHistogram("livepeer_protocol_reward_duration_seconds", metrics.Labels{}, dur)
	}()

	if round.Number > s.cfg.PurgeWindow {
		_, _ = s.cfg.Cache.PurgeBefore(round.Number - s.cfg.PurgeWindow)
	}

	// 1) Eligibility (active / not-already-rewarded / round initialized).
	elig, skip, err := s.evalEligibility(ctx, round)
	if err != nil {
		return ForceResult{}, err
	}
	if skip != nil {
		return s.declineForce(round, elig, skip), nil
	}

	// 2) Build the calldata for the (possibly fresh) tx.
	hints, calldata, err := s.buildRewardCalldata(ctx, round.Number)
	if err != nil {
		return ForceResult{}, err
	}

	// 3) Inspect any existing intent for this (round, orch). An in-flight one
	//    must not be double-broadcast; a confirmed one is done; a failed one is
	//    exactly what Force is here to re-drive.
	id := txintent.ComputeID("RewardWithHint", rewardKey(round.Number, s.cfg.OrchAddress))
	prior, priorErr := s.cfg.TxIntent.Status(ctx, id)
	priorFailed := false
	if priorErr == nil {
		switch {
		case !prior.Status.IsTerminal():
			reason := "a reward tx for this round is already pending — wait for it to confirm before forcing again"
			if a := prior.CurrentAttempt(); a != nil {
				reason = fmt.Sprintf("a reward tx for this round is already pending (tx %s) — wait for it to confirm before forcing again", a.SignedTxHash.Hex())
			}
			return s.declineForce(round, elig, &SkipReason{Reason: reason, Code: SkipCodeRewardInFlight}), nil
		case prior.Status == txintent.StatusConfirmed:
			reason := fmt.Sprintf("reward for round %d already confirmed on-chain — nothing to do", round.Number)
			return s.declineForce(round, elig, &SkipReason{Reason: reason, Code: SkipCodeAlreadyRewarded}), nil
		default: // StatusFailed
			priorFailed = true
		}
	}

	// 4) Pre-send guards (free, no gas): wallet can afford gas, and reward()
	//    won't revert. Only run when a Caller is wired.
	if s.cfg.Caller != nil {
		if guard := s.preflight(ctx, calldata); guard != nil {
			return s.declineForce(round, elig, guard), nil
		}
	}

	// 5) Broadcast a NEW tx: re-drive the failed intent (fresh calldata) past
	//    idempotency, or submit a new one. Either way this spends gas.
	var intentID txintent.IntentID
	if priorFailed {
		if err := s.cfg.TxIntent.Resubmit(ctx, id, calldata); err != nil {
			return ForceResult{}, fmt.Errorf("%s: %w", types.ErrCodeRewardSubmitFailed, err)
		}
		intentID = id
	} else {
		intentID, err = s.cfg.TxIntent.Submit(ctx, s.rewardParams(round.Number, calldata))
		if err != nil {
			return ForceResult{}, fmt.Errorf("%s: %w", types.ErrCodeRewardSubmitFailed, err)
		}
	}

	s.recordSuccess(round, elig, intentID)
	s.metricsCounter("livepeer_protocol_reward_total", metrics.Labels{"outcome": "force_submitted"}, 1)
	s.metricsGauge("livepeer_protocol_eligible_round_count", metrics.Labels{}, 1)
	s.metricsGauge("livepeer_protocol_active_status", metrics.Labels{}, 1)
	if s.cfg.Logger != nil {
		s.cfg.Logger.Warn("force reward: broadcasting a new reward transaction (gas will be spent)",
			logger.Uint64("round", uint64(round.Number)),
			logger.String("intent_id", intentID.Hex()),
			logger.String("prev", hints.Prev.Hex()),
			logger.String("next", hints.Next.Hex()),
		)
	}
	return ForceResult{IntentID: intentID}, nil
}

// declineForce records the skip outcome and returns the typed result. Used by
// every force-path branch that chooses not to broadcast.
func (s *Service) declineForce(round chain.Round, elig types.RewardEligibility, skip *SkipReason) ForceResult {
	if elig.Reason == "" {
		elig.Reason = skip.Reason
	}
	s.recordSkip(round, elig)
	s.metricsCounter("livepeer_protocol_reward_total", metrics.Labels{"outcome": "force_skipped"}, 1)
	s.metricsGauge("livepeer_protocol_active_status", metrics.Labels{}, boolFloat(elig.Active))
	if s.cfg.Logger != nil {
		s.cfg.Logger.Info("force reward declined (no tx sent, no gas spent)",
			logger.Uint64("round", uint64(round.Number)),
			logger.String("reason", skip.Reason),
		)
	}
	return ForceResult{Skip: skip}
}

// preflight runs the force path's free pre-send checks via the Caller: it
// confirms the wallet can cover the gas, then dry-runs reward() with eth_call
// so a guaranteed revert costs no gas. Returns a non-nil Skip (with a clear
// reason) when the tx should not be broadcast; nil means "go ahead". Transient
// read errors on the balance check are ignored — they must not block a
// legitimate force.
func (s *Service) preflight(ctx context.Context, calldata []byte) *SkipReason {
	if bal, err := s.cfg.Caller.BalanceAt(ctx, s.cfg.OrchAddress, nil); err == nil && bal != nil {
		if price, perr := s.cfg.Caller.SuggestGasPrice(ctx); perr == nil && price != nil {
			cost := new(big.Int).Mul(price, new(big.Int).SetUint64(s.cfg.GasLimit))
			if bal.Cmp(cost) < 0 {
				return &SkipReason{
					Reason: fmt.Sprintf("orchestrator wallet balance (%s wei) is below the reward tx gas cost (~%s wei) — top up the wallet", bal.String(), cost.String()),
					Code:   SkipCodeInsufficientBalance,
				}
			}
		}
	}
	to := s.cfg.BondingManager.Address()
	if _, err := s.cfg.Caller.CallContract(ctx, ethereum.CallMsg{
		From: s.cfg.OrchAddress,
		To:   &to,
		Data: calldata,
	}, nil); err != nil {
		return &SkipReason{
			Reason: fmt.Sprintf("reward() would revert on-chain (%s) — not sending, no gas spent", err.Error()),
			Code:   SkipCodeRewardWouldRevert,
		}
	}
	return nil
}

// buildRewardCalldata computes the pool-position hints and packs the
// rewardWithHint calldata for the round.
func (s *Service) buildRewardCalldata(ctx context.Context, round chain.RoundNumber) (types.PoolHints, []byte, error) {
	hints, err := s.computeHints(ctx, round)
	if err != nil {
		return types.PoolHints{}, nil, fmt.Errorf("%s: %w", types.ErrCodeRewardPoolWalkFailed, err)
	}
	calldata, err := s.cfg.BondingManager.PackRewardWithHint(hints.Prev, hints.Next)
	if err != nil {
		return types.PoolHints{}, nil, fmt.Errorf("PackRewardWithHint: %w", err)
	}
	return hints, calldata, nil
}

// rewardParams builds the TxIntent submit params for a reward tx.
func (s *Service) rewardParams(round chain.RoundNumber, calldata []byte) txintent.Params {
	return txintent.Params{
		Kind:      "RewardWithHint",
		KeyParams: rewardKey(round, s.cfg.OrchAddress),
		To:        s.cfg.BondingManager.Address(),
		CallData:  calldata,
		Value:     new(big.Int),
		GasLimit:  s.cfg.GasLimit,
		Metadata: map[string]string{
			"round": fmt.Sprintf("%d", round),
			"orch":  s.cfg.OrchAddress.Hex(),
		},
	}
}

// computeHints returns the (prev, next) hints for the configured orch in the
// transcoder pool at this round. Cache hit short-circuits the walk.
func (s *Service) computeHints(ctx context.Context, round chain.RoundNumber) (types.PoolHints, error) {
	if cached, ok, err := s.cfg.Cache.Get(round, s.cfg.OrchAddress); err == nil && ok {
		return cached, nil
	} else if err != nil {
		// Cache read failure is non-fatal; log and proceed to walk.
		if s.cfg.Logger != nil {
			s.cfg.Logger.Warn("pool-hint cache read failed",
				logger.Uint64("round", uint64(round)),
				logger.Err(err),
			)
		}
	}

	hints, err := s.walkPool(ctx)
	if err != nil {
		return types.PoolHints{}, err
	}
	if err := s.cfg.Cache.Put(round, s.cfg.OrchAddress, hints); err != nil {
		// Cache write failure is non-fatal; log and proceed.
		if s.cfg.Logger != nil {
			s.cfg.Logger.Warn("pool-hint cache write failed",
				logger.Uint64("round", uint64(round)),
				logger.Err(err),
			)
		}
	}
	return hints, nil
}

// walkPool returns the (prev, next) addresses surrounding the configured
// orchestrator in the BondingManager linked-list pool. Returns
// PoolHints{} when the orch isn't in the pool (caller treats as
// ineligible / not-yet-active).
func (s *Service) walkPool(ctx context.Context) (types.PoolHints, error) {
	cur, err := s.cfg.BondingManager.GetFirstTranscoderInPool(ctx)
	if err != nil {
		return types.PoolHints{}, err
	}
	var prev chain.Address
	for cur != (chain.Address{}) {
		next, err := s.cfg.BondingManager.GetNextTranscoderInPool(ctx, cur)
		if err != nil {
			return types.PoolHints{}, err
		}
		if cur == s.cfg.OrchAddress {
			return types.PoolHints{Prev: prev, Next: next}, nil
		}
		prev = cur
		cur = next
	}
	// Orch not in pool — return zero hints. rewardWithHint(0, 0) typically
	// reverts in this case; caller should have skipped via eligibility.
	return types.PoolHints{}, nil
}

// ParseEarnings extracts the earned reward amount for the configured orch
// from the receipt logs. Returns (zero, false) if no Reward event is
// present for the orch (e.g., reverted tx, malformed logs).
func (s *Service) ParseEarnings(logs []ethtypes.Log) (*big.Int, bool) {
	return bondingmanager.FindRewardForTranscoder(logs, s.cfg.OrchAddress)
}

// rewardKey returns the canonical KeyParams bytes for a (round, orch) tuple.
func rewardKey(round chain.RoundNumber, orchAddr chain.Address) []byte {
	out := make([]byte, 8+20)
	copy(out[:8], round.Bytes())
	copy(out[8:], orchAddr[:])
	return out
}

// Status snapshots the service's last-observed state.
type Status struct {
	LastRound       chain.RoundNumber
	LastEligibility *types.RewardEligibility
	LastIntent      *txintent.IntentID
	LastEarnedWei   *big.Int
	LastError       string
}

// Status returns the most recent (round, eligibility, intent, earned, err).
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{
		LastRound:       s.lastRound,
		LastEligibility: copyEligibility(s.lastEligibility),
		LastIntent:      copyID(s.lastIntent),
	}
	if s.lastEarnedWei != nil {
		st.LastEarnedWei = new(big.Int).Set(s.lastEarnedWei)
	}
	if s.lastErr != nil {
		st.LastError = s.lastErr.Error()
	}
	return st
}

// SetEarnings is called by the lifecycle once a confirmed receipt arrives,
// to record the parsed earned amount on the service status. Kept on the
// public surface so the lifecycle can update without re-reading receipts.
func (s *Service) SetEarnings(round chain.RoundNumber, amount *big.Int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastRound == round {
		if amount != nil {
			s.lastEarnedWei = new(big.Int).Set(amount)
		}
	}
	s.metricsCounter("livepeer_protocol_reward_earned_wei_total",
		metrics.Labels{}, weiFloat(amount))
}

func (s *Service) recordSuccess(round chain.Round, elig types.RewardEligibility, id txintent.IntentID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRound = round.Number
	e := elig
	s.lastEligibility = &e
	idCopy := id
	s.lastIntent = &idCopy
	s.lastErr = nil
}

func (s *Service) recordSkip(round chain.Round, elig types.RewardEligibility) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRound = round.Number
	e := elig
	s.lastEligibility = &e
	s.lastIntent = nil
	s.lastErr = nil
}

func (s *Service) recordError(round chain.Round, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRound = round.Number
	s.lastErr = err
}

func copyEligibility(e *types.RewardEligibility) *types.RewardEligibility {
	if e == nil {
		return nil
	}
	cp := *e
	return &cp
}

func copyID(id *txintent.IntentID) *txintent.IntentID {
	if id == nil {
		return nil
	}
	cp := *id
	return &cp
}

// failedReason renders a terminal-failed intent's reason for logging, or a
// placeholder when the reason is absent.
func failedReason(t txintent.TxIntent) string {
	if t.FailedReason != nil {
		return t.FailedReason.Error()
	}
	return "unknown"
}

func boolFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func weiFloat(amount *big.Int) float64 {
	if amount == nil {
		return 0
	}
	// Loss of precision is acceptable for metric counters; precise
	// accounting lives in audit logs / TxIntent metadata.
	f, _ := new(big.Float).SetInt(amount).Float64()
	return f
}

func (s *Service) metricsCounter(name string, labels metrics.Labels, delta float64) {
	if s.cfg.Metrics == nil {
		return
	}
	s.cfg.Metrics.CounterAdd(name, labels, delta)
}

func (s *Service) metricsGauge(name string, labels metrics.Labels, value float64) {
	if s.cfg.Metrics == nil {
		return
	}
	s.cfg.Metrics.GaugeSet(name, labels, value)
}

func (s *Service) metricsHistogram(name string, labels metrics.Labels, value float64) {
	if s.cfg.Metrics == nil {
		return
	}
	s.cfg.Metrics.HistogramObserve(name, labels, value)
}
