// Package roundinit implements the round-init service.
//
// Subscribes to typed Round events from chain-commons.services.roundclock,
// checks RoundsManager.currentRoundInitialized for each new round, and
// (if not initialized) submits an "InitializeRound" TxIntent with optional
// random jitter. Idempotency is content-addressed by round number; the
// chain-commons TxIntent state machine ensures a duplicate submit is a no-op.
//
// Pattern recorded in docs/exec-plans/completed/0020-protocol-daemon-migration.md §7.
package roundinit

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/roundclock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/txintent"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/types"
)

// RoundsManager is the subset of the ABI binding the service depends on.
// Defining it as an interface here lets tests use a stub without importing
// the providers package.
type RoundsManager interface {
	Address() chain.Address
	CurrentRoundInitialized(ctx context.Context) (bool, error)
	PackInitializeRound() ([]byte, error)
}

// TxSubmitter is the subset of chain-commons.txintent.Manager the service
// uses. Allows test stubs without spinning up a full Manager + Processor.
type TxSubmitter interface {
	Submit(ctx context.Context, p txintent.Params) (txintent.IntentID, error)
	Status(ctx context.Context, id txintent.IntentID) (txintent.TxIntent, error)
	Wait(ctx context.Context, id txintent.IntentID) (txintent.TxIntent, error)
}

// Config holds Service dependencies. All fields are required.
type Config struct {
	RoundsManager RoundsManager
	TxIntent      TxSubmitter
	Clock         clock.Clock
	GasLimit      uint64
	InitJitter    time.Duration
	Logger        logger.Logger
	Metrics       metrics.Recorder

	// Random source for jitter. Allows deterministic tests.
	Rand *rand.Rand
}

// SkipCode is a stable machine identifier for why a force-action did
// not fire a transaction. Numeric values mirror
// protocolv1.SkipReason_Code; keep the two in sync.
type SkipCode uint32

const (
	// SkipCodeUnspecified is the zero value (not used in current code
	// paths; reserved for forward-compat).
	SkipCodeUnspecified SkipCode = 0

	// SkipCodeRoundInitialized indicates the round was already
	// initialized on-chain when the operator triggered the force.
	// Matches protocolv1.SkipReason_CODE_ROUND_INITIALIZED = 3.
	SkipCodeRoundInitialized SkipCode = 3
)

// SkipReason carries why TryInitialize did not submit a tx.
type SkipReason struct {
	Reason string
	Code   SkipCode
}

// ForceResult is the outcome of an operator-triggered TryInitialize.
// Exactly one of IntentID or Skip is set on success: Skip != nil for a
// short-circuit (no tx fired); otherwise IntentID is the submitted tx.
// On error (returned alongside), ForceResult is the zero value.
type ForceResult struct {
	IntentID txintent.IntentID
	Skip     *SkipReason
}

// Service is the round-init service.
type Service struct {
	cfg Config

	mu                 sync.Mutex
	lastRound          chain.RoundNumber
	currentInitialized bool
	lastIntent         *txintent.IntentID
	lastErr            error
}

// New constructs a Service. Validates required dependencies.
func New(cfg Config) (*Service, error) {
	if cfg.RoundsManager == nil {
		return nil, errors.New("roundinit: RoundsManager is required")
	}
	if cfg.TxIntent == nil {
		return nil, errors.New("roundinit: TxIntent is required")
	}
	if cfg.GasLimit == 0 {
		return nil, errors.New("roundinit: GasLimit is required (>0)")
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.System()
	}
	if cfg.Metrics == nil {
		cfg.Metrics = metrics.NoOp()
	}
	if cfg.Rand == nil {
		// Per-service random source seeded from the system clock — fine
		// for jitter (no cryptographic requirement).
		cfg.Rand = rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec
	}
	return &Service{cfg: cfg}, nil
}

// Run subscribes to Round events from rc and processes each. Returns when
// the channel is closed or ctx is cancelled.
func (s *Service) Run(ctx context.Context, rc roundclock.Clock) error {
	rounds, err := rc.SubscribeRounds(ctx)
	if err != nil {
		return fmt.Errorf("roundinit: subscribe rounds: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case r, ok := <-rounds:
			if !ok {
				return nil
			}
			if err := s.tryInitialize(ctx, r); err != nil {
				s.recordError(r, err)
				s.metricsCounter("livepeer_protocol_round_init_total",
					metrics.Labels{"outcome": "error"}, 1)
				if s.cfg.Logger != nil {
					s.cfg.Logger.Warn("round-init failed",
						logger.Uint64("round", uint64(r.Number)),
						logger.Err(err),
					)
				}
				continue
			}
		}
	}
}

// TryInitialize is the operator-callable entry point for ForceInitializeRound.
// Returns either an IntentID (a tx was submitted) or a typed Skip (no
// tx fired — the round was already initialized). On error, the result
// is the zero ForceResult.
func (s *Service) TryInitialize(ctx context.Context, round chain.Round) (ForceResult, error) {
	return s.tryInitializeForce(ctx, round)
}

// tryInitialize handles one Round event from the Run loop. Returns nil
// when the round is either already initialized or successfully
// submitted; an error otherwise.
func (s *Service) tryInitialize(ctx context.Context, round chain.Round) error {
	_, err := s.tryInitializeForce(ctx, round)
	return err
}

func (s *Service) tryInitializeForce(ctx context.Context, round chain.Round) (ForceResult, error) {
	start := s.cfg.Clock.Now()
	defer func() {
		dur := s.cfg.Clock.Now().Sub(start).Seconds()
		s.metricsHistogram("livepeer_protocol_round_init_duration_seconds",
			metrics.Labels{}, dur)
	}()

	initialized, err := s.cfg.RoundsManager.CurrentRoundInitialized(ctx)
	if err != nil {
		return ForceResult{}, fmt.Errorf("currentRoundInitialized: %w", err)
	}
	if initialized {
		s.metricsCounter("livepeer_protocol_round_init_total",
			metrics.Labels{"outcome": "skipped"}, 1)
		if s.cfg.Logger != nil {
			s.cfg.Logger.Debug("round already initialized",
				logger.Uint64("round", uint64(round.Number)),
				logger.String("reason", types.ErrCodeRoundInitAlreadyInitialized),
			)
		}
		s.recordObservation(round, true, nil)
		return ForceResult{Skip: &SkipReason{
			Reason: "round already initialized",
			Code:   SkipCodeRoundInitialized,
		}}, nil
	}

	if s.cfg.InitJitter > 0 {
		jitterNanos := s.cfg.Rand.Int63n(int64(s.cfg.InitJitter))
		if jitterNanos > 0 {
			if err := s.cfg.Clock.Sleep(ctx, time.Duration(jitterNanos)); err != nil {
				if s.cfg.Logger != nil {
					s.cfg.Logger.Debug("init jitter cancelled",
						logger.String("reason", types.ErrCodeRoundInitJitterCancelled),
						logger.Err(err),
					)
				}
				return ForceResult{}, err
			}
		}
	}

	calldata, err := s.cfg.RoundsManager.PackInitializeRound()
	if err != nil {
		return ForceResult{}, fmt.Errorf("PackInitializeRound: %w", err)
	}

	intentID, err := s.cfg.TxIntent.Submit(ctx, txintent.Params{
		Kind:      "InitializeRound",
		KeyParams: round.Number.Bytes(),
		To:        s.cfg.RoundsManager.Address(),
		CallData:  calldata,
		Value:     new(big.Int),
		GasLimit:  s.cfg.GasLimit,
		Metadata: map[string]string{
			"round": fmt.Sprintf("%d", round.Number),
		},
	})
	if err != nil {
		return ForceResult{}, fmt.Errorf("%s: %w", types.ErrCodeRoundInitSubmitFailed, err)
	}

	// We just submitted; on-chain state is still uninitialized until the
	// tx confirms. Operators reading Status will see
	// (CurrentInitialized=false, LastIntent=<id>) — the in-flight signal.
	s.recordObservation(round, false, &intentID)
	s.metricsCounter("livepeer_protocol_round_init_total",
		metrics.Labels{"outcome": "submitted"}, 1)
	if s.cfg.Logger != nil {
		s.cfg.Logger.Info("round-init submitted",
			logger.Uint64("round", uint64(round.Number)),
			logger.String("intent_id", intentID.Hex()),
		)
	}
	return ForceResult{IntentID: intentID}, nil
}

// Status returns the most recent (round, currentInitialized, intent, err)
// snapshot the service has observed. Used by the gRPC GetRoundStatus
// handler.
type Status struct {
	LastRound          chain.RoundNumber
	CurrentInitialized bool
	LastIntent         *txintent.IntentID
	LastError          string
}

// Status snapshots the service's last-observed state.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{
		LastRound:          s.lastRound,
		CurrentInitialized: s.currentInitialized,
		LastIntent:         copyID(s.lastIntent),
	}
	if s.lastErr != nil {
		st.LastError = s.lastErr.Error()
	}
	return st
}

// recordObservation captures one round's observation: the round we just
// processed, whether the on-chain state already had it initialized at
// the moment we queried, and (when we submitted) the resulting intent.
func (s *Service) recordObservation(round chain.Round, initialized bool, id *txintent.IntentID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRound = round.Number
	s.currentInitialized = initialized
	s.lastIntent = copyID(id)
	s.lastErr = nil
}

func (s *Service) recordError(round chain.Round, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRound = round.Number
	s.lastErr = err
}

func copyID(id *txintent.IntentID) *txintent.IntentID {
	if id == nil {
		return nil
	}
	out := *id
	return &out
}

func (s *Service) metricsCounter(name string, labels metrics.Labels, delta float64) {
	if s.cfg.Metrics == nil {
		return
	}
	s.cfg.Metrics.CounterAdd(name, labels, delta)
}

func (s *Service) metricsHistogram(name string, labels metrics.Labels, value float64) {
	if s.cfg.Metrics == nil {
		return
	}
	s.cfg.Metrics.HistogramObserve(name, labels, value)
}
