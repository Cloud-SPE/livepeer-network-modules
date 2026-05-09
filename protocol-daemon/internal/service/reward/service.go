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

	ethtypes "github.com/ethereum/go-ethereum/core/types"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/roundclock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/txintent"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/providers/bondingmanager"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/types"
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

	OrchAddress chain.Address
	GasLimit    uint64

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

// TryReward is the operator-callable entry point for ForceRewardCall.
// Returns either an IntentID (a tx was submitted) or a typed Skip (no
// tx fired — already rewarded / inactive). On error, the result is the
// zero ForceResult.
func (s *Service) TryReward(ctx context.Context, round chain.Round) (ForceResult, error) {
	return s.tryRewardForce(ctx, round)
}

// tryReward handles one round driven by the Run loop. Returns nil on
// skipped (ineligible) or successful submit; an error otherwise.
func (s *Service) tryReward(ctx context.Context, round chain.Round) error {
	_, err := s.tryRewardForce(ctx, round)
	return err
}

func (s *Service) tryRewardForce(ctx context.Context, round chain.Round) (ForceResult, error) {
	start := s.cfg.Clock.Now()
	defer func() {
		dur := s.cfg.Clock.Now().Sub(start).Seconds()
		s.metricsHistogram("livepeer_protocol_reward_duration_seconds",
			metrics.Labels{}, dur)
	}()

	// Purge old cache entries to keep the store bounded. Best-effort —
	// errors here don't stop the reward attempt.
	if round.Number > s.cfg.PurgeWindow {
		_, _ = s.cfg.Cache.PurgeBefore(round.Number - s.cfg.PurgeWindow)
	}

	tinfo, err := s.cfg.BondingManager.GetTranscoder(ctx, s.cfg.OrchAddress)
	if err != nil {
		return ForceResult{}, fmt.Errorf("getTranscoder: %w", err)
	}

	elig := types.RewardEligibility{
		OrchestratorAddress: s.cfg.OrchAddress,
		Round:               round.Number,
		Active:              tinfo.IsActiveAtRound(round.Number),
		LastRewardRound:     tinfo.LastRewardRound,
	}
	elig.Eligible = elig.Active && tinfo.LastRewardRound < round.Number
	if !elig.Eligible {
		var skipCode SkipCode
		switch {
		case !elig.Active:
			elig.Reason = "transcoder is not active at this round"
			skipCode = SkipCodeTranscoderInactive
		case tinfo.LastRewardRound >= round.Number:
			elig.Reason = "already rewarded this round"
			skipCode = SkipCodeAlreadyRewarded
		default:
			elig.Reason = "ineligible"
			skipCode = SkipCodeUnspecified
		}
		s.recordSkip(round, elig)
		s.metricsCounter("livepeer_protocol_reward_total",
			metrics.Labels{"outcome": "skipped"}, 1)
		s.metricsGauge("livepeer_protocol_eligible_round_count", metrics.Labels{}, 0)
		s.metricsGauge("livepeer_protocol_active_status", metrics.Labels{}, boolFloat(elig.Active))
		if s.cfg.Logger != nil {
			s.cfg.Logger.Debug("reward skipped",
				logger.Uint64("round", uint64(round.Number)),
				logger.String("reason", elig.Reason),
			)
		}
		return ForceResult{Skip: &SkipReason{Reason: elig.Reason, Code: skipCode}}, nil
	}

	hints, err := s.computeHints(ctx, round.Number)
	if err != nil {
		return ForceResult{}, fmt.Errorf("%s: %w", types.ErrCodeRewardPoolWalkFailed, err)
	}

	calldata, err := s.cfg.BondingManager.PackRewardWithHint(hints.Prev, hints.Next)
	if err != nil {
		return ForceResult{}, fmt.Errorf("PackRewardWithHint: %w", err)
	}

	intentID, err := s.cfg.TxIntent.Submit(ctx, txintent.Params{
		Kind:      "RewardWithHint",
		KeyParams: rewardKey(round.Number, s.cfg.OrchAddress),
		To:        s.cfg.BondingManager.Address(),
		CallData:  calldata,
		Value:     new(big.Int),
		GasLimit:  s.cfg.GasLimit,
		Metadata: map[string]string{
			"round": fmt.Sprintf("%d", round.Number),
			"orch":  s.cfg.OrchAddress.Hex(),
		},
	})
	if err != nil {
		return ForceResult{}, fmt.Errorf("%s: %w", types.ErrCodeRewardSubmitFailed, err)
	}

	s.recordSuccess(round, elig, intentID)
	s.metricsCounter("livepeer_protocol_reward_total",
		metrics.Labels{"outcome": "submitted"}, 1)
	s.metricsGauge("livepeer_protocol_eligible_round_count", metrics.Labels{}, 1)
	s.metricsGauge("livepeer_protocol_active_status", metrics.Labels{}, 1)
	if s.cfg.Logger != nil {
		s.cfg.Logger.Info("reward submitted",
			logger.Uint64("round", uint64(round.Number)),
			logger.String("intent_id", intentID.Hex()),
			logger.String("prev", hints.Prev.Hex()),
			logger.String("next", hints.Next.Hex()),
		)
	}

	return ForceResult{IntentID: intentID}, nil
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
