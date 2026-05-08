// Package grpc holds the Go-native gRPC handler surface for protocol-daemon.
//
// Method shapes mirror proto/livepeer/protocol/v1/protocol.proto exactly.
// The actual gRPC binding (protoc-gen-go output) lands as a follow-up
// (matches service-registry-daemon's plan-0001 → 0002 split). For now
// this package is the in-process integration target.
//
// All RPCs are operator-facing and unauthenticated; the daemon binds
// only to a unix socket so the trust boundary is the local user.
package grpc

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/roundclock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/txintent"
	aiserviceregistrysvc "github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/aiserviceregistry"
	orchstatussvc "github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/orchstatus"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/reward"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/roundinit"
	serviceregistrysvc "github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/serviceregistry"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/types"
)

// ErrUnimplemented mirrors codes.Unimplemented for the Go-native API.
// The gRPC adapter (lands in a follow-up) translates this to a real
// gRPC status. Tests assert errors.Is(err, ErrUnimplemented).
var ErrUnimplemented = errors.New("not implemented in this mode")

// ErrNotFound is returned by GetTxIntent when the IntentID doesn't
// resolve to a stored intent.
var ErrNotFound = errors.New("not found")

// ErrInvalidArgument mirrors codes.InvalidArgument for the Go-native API.
var ErrInvalidArgument = errors.New("invalid argument")

// HealthStatus mirrors proto.HealthStatus.
type HealthStatus struct {
	OK      bool
	Mode    string
	Version string
	ChainID uint64
}

// RoundEvent mirrors proto.RoundEvent.
type RoundEvent struct {
	Number       uint64
	StartBlock   uint64
	L1StartBlock uint64
	Length       uint64
	Initialized  bool
	BlockHash    [32]byte
}

// RoundStatus mirrors proto.RoundStatus.
type RoundStatus struct {
	LastRound               uint64
	LastIntentID            []byte
	LastError               string
	CurrentRoundInitialized bool
}

// RewardStatus mirrors proto.RewardStatus.
type RewardStatus struct {
	LastRound         uint64
	OrchAddress       chain.Address
	Eligible          bool
	EligibilityReason string
	LastRewardRound   uint64
	Active            bool
	LastIntentID      []byte
	LastEarnedWei     *big.Int
	LastError         string
}

// TxIntentRef mirrors proto.TxIntentRef.
type TxIntentRef struct {
	ID [32]byte
}

// SetServiceURIRequest mirrors proto.SetServiceURIRequest.
type SetServiceURIRequest struct {
	URL string
}

// SetAIServiceURIRequest mirrors proto.SetAIServiceURIRequest.
type SetAIServiceURIRequest struct {
	URL string
}

// OnChainServiceURIStatus mirrors proto.OnChainServiceURIStatus.
type OnChainServiceURIStatus struct {
	URL string
}

// OnChainAIServiceURIStatus mirrors proto.OnChainAIServiceURIStatus.
type OnChainAIServiceURIStatus struct {
	URL string
}

// RegistrationStatus mirrors proto.RegistrationStatus.
type RegistrationStatus struct {
	Registered bool
}

// AIRegistrationStatus mirrors proto.AIRegistrationStatus.
type AIRegistrationStatus struct {
	Registered bool
}

// WalletBalanceStatus mirrors proto.WalletBalanceStatus.
type WalletBalanceStatus struct {
	WalletAddress chain.Address
	BalanceWei    *big.Int
}

// SkipCode mirrors protocolv1.SkipReason_Code. Numeric values are kept
// in sync with the proto enum so the convert layer is a one-line cast.
type SkipCode uint32

const (
	// SkipCodeUnspecified is the zero value (forward-compat slot).
	SkipCodeUnspecified SkipCode = 0

	// SkipCodeAlreadyRewarded — orch already rewarded this round.
	SkipCodeAlreadyRewarded SkipCode = 1

	// SkipCodeTranscoderInactive — transcoder is not active at this round.
	SkipCodeTranscoderInactive SkipCode = 2

	// SkipCodeRoundInitialized — round already initialized on-chain.
	SkipCodeRoundInitialized SkipCode = 3
)

// SkipReason mirrors proto.SkipReason. Returned inside ForceOutcome
// when the daemon short-circuits a force-action.
type SkipReason struct {
	Reason string
	Code   SkipCode
}

// ForceOutcome mirrors proto.ForceOutcome. Exactly one of Submitted or
// Skipped is non-nil on success. Submitted carries the IntentID of a
// freshly-submitted tx; Skipped carries a typed reason for the no-op.
type ForceOutcome struct {
	Submitted *TxIntentRef
	Skipped   *SkipReason
}

// TxIntentSnapshot mirrors proto.TxIntentSnapshot.
type TxIntentSnapshot struct {
	ID                    [32]byte
	Kind                  string
	Status                string
	FailedClass           string
	FailedCode            string
	FailedMessage         string
	CreatedAtUnixNano     int64
	LastUpdatedAtUnixNano int64
	ConfirmedAtUnixNano   int64
	AttemptCount          uint32
}

// RoundClockSource is the subset of roundclock.Clock the streaming RPC uses.
type RoundClockSource interface {
	SubscribeRounds(ctx context.Context) (<-chan chain.Round, error)
}

// Compile-time: chain-commons RoundClock satisfies RoundClockSource.
var _ RoundClockSource = (roundclock.Clock)(nil)

// TxIntentReader is the subset of chain-commons.txintent.Manager used here.
type TxIntentReader interface {
	Status(ctx context.Context, id txintent.IntentID) (txintent.TxIntent, error)
}

// Server is the Go-native handler set for ProtocolDaemon.
type Server struct {
	mode    types.Mode
	version string
	chainID uint64

	roundInit  *roundinit.Service // nil when --mode=reward
	reward     *reward.Service    // nil when --mode=round-init
	registry   *serviceregistrysvc.Service
	orch       *orchstatussvc.Service
	aiRegistry *aiserviceregistrysvc.Service
	aiOrch     *orchstatussvc.Service

	tx TxIntentReader
	rc RoundClockSource
}

// Config wires Server dependencies.
type Config struct {
	Mode    types.Mode
	Version string
	ChainID uint64

	RoundInit  *roundinit.Service
	Reward     *reward.Service
	Registry   *serviceregistrysvc.Service
	Orch       *orchstatussvc.Service
	AIRegistry *aiserviceregistrysvc.Service
	AIOrch     *orchstatussvc.Service
	Tx         TxIntentReader
	RC         RoundClockSource
}

// New constructs a Server. Mode-specific services are required if the
// mode covers them.
func New(cfg Config) (*Server, error) {
	if err := cfg.Mode.Validate(); err != nil {
		return nil, err
	}
	if cfg.Mode.HasRoundInit() && cfg.RoundInit == nil {
		return nil, errors.New("grpc: RoundInit service is required for round-init mode")
	}
	if cfg.Mode.HasReward() && cfg.Reward == nil {
		return nil, errors.New("grpc: Reward service is required for reward mode")
	}
	return &Server{
		mode:       cfg.Mode,
		version:    cfg.Version,
		chainID:    cfg.ChainID,
		roundInit:  cfg.RoundInit,
		reward:     cfg.Reward,
		registry:   cfg.Registry,
		orch:       cfg.Orch,
		aiRegistry: cfg.AIRegistry,
		aiOrch:     cfg.AIOrch,
		tx:         cfg.Tx,
		rc:         cfg.RC,
	}, nil
}

// Health returns the daemon's health snapshot. Always implemented.
func (s *Server) Health(_ context.Context, _ struct{}) (HealthStatus, error) {
	return HealthStatus{
		OK:      true,
		Mode:    s.mode.String(),
		Version: s.version,
		ChainID: s.chainID,
	}, nil
}

// GetRoundStatus implements the round-status RPC.
func (s *Server) GetRoundStatus(_ context.Context, _ struct{}) (RoundStatus, error) {
	if !s.mode.HasRoundInit() {
		return RoundStatus{}, ErrUnimplemented
	}
	st := s.roundInit.Status()
	out := RoundStatus{
		LastRound:               uint64(st.LastRound),
		LastError:               st.LastError,
		CurrentRoundInitialized: st.CurrentInitialized,
	}
	if st.LastIntent != nil {
		out.LastIntentID = st.LastIntent[:]
	}
	return out, nil
}

// GetRewardStatus implements the reward-status RPC.
func (s *Server) GetRewardStatus(_ context.Context, _ struct{}) (RewardStatus, error) {
	if !s.mode.HasReward() {
		return RewardStatus{}, ErrUnimplemented
	}
	st := s.reward.Status()
	out := RewardStatus{
		LastRound: uint64(st.LastRound),
		LastError: st.LastError,
	}
	if st.LastEligibility != nil {
		out.OrchAddress = st.LastEligibility.OrchestratorAddress
		out.Eligible = st.LastEligibility.Eligible
		out.EligibilityReason = st.LastEligibility.Reason
		out.LastRewardRound = uint64(st.LastEligibility.LastRewardRound)
		out.Active = st.LastEligibility.Active
	}
	if st.LastIntent != nil {
		out.LastIntentID = st.LastIntent[:]
	}
	if st.LastEarnedWei != nil {
		out.LastEarnedWei = new(big.Int).Set(st.LastEarnedWei)
	}
	return out, nil
}

// ForceInitializeRound triggers the round-init handler immediately.
// Returns either the IntentID of a submitted tx (Submitted) or a
// typed skip reason (Skipped) when the daemon short-circuited.
func (s *Server) ForceInitializeRound(ctx context.Context, _ struct{}) (ForceOutcome, error) {
	if !s.mode.HasRoundInit() {
		return ForceOutcome{}, ErrUnimplemented
	}
	// Use the round-init service's status for the round number; if the
	// service hasn't seen one yet, force-initialize round 0 with a
	// best-effort marker.
	st := s.roundInit.Status()
	res, err := s.roundInit.TryInitialize(ctx, chain.Round{Number: st.LastRound})
	if err != nil {
		return ForceOutcome{}, err
	}
	return forceOutcomeFromRoundInit(res), nil
}

// ForceRewardCall triggers the reward handler immediately. Returns
// either the IntentID of a submitted tx (Submitted) or a typed skip
// reason (Skipped) when the orch was ineligible.
func (s *Server) ForceRewardCall(ctx context.Context, _ struct{}) (ForceOutcome, error) {
	if !s.mode.HasReward() {
		return ForceOutcome{}, ErrUnimplemented
	}
	st := s.reward.Status()
	res, err := s.reward.TryReward(ctx, chain.Round{Number: st.LastRound})
	if err != nil {
		return ForceOutcome{}, err
	}
	return forceOutcomeFromReward(res), nil
}

func forceOutcomeFromReward(r reward.ForceResult) ForceOutcome {
	if r.Skip != nil {
		return ForceOutcome{Skipped: &SkipReason{
			Reason: r.Skip.Reason,
			Code:   SkipCode(r.Skip.Code),
		}}
	}
	return ForceOutcome{Submitted: &TxIntentRef{ID: r.IntentID}}
}

func forceOutcomeFromRoundInit(r roundinit.ForceResult) ForceOutcome {
	if r.Skip != nil {
		return ForceOutcome{Skipped: &SkipReason{
			Reason: r.Skip.Reason,
			Code:   SkipCode(r.Skip.Code),
		}}
	}
	return ForceOutcome{Submitted: &TxIntentRef{ID: r.IntentID}}
}

// SetServiceURI submits a ServiceRegistry.setServiceURI txintent.
func (s *Server) SetServiceURI(ctx context.Context, req SetServiceURIRequest) (TxIntentRef, error) {
	if s.registry == nil {
		return TxIntentRef{}, ErrUnimplemented
	}
	if req.URL == "" {
		return TxIntentRef{}, ErrInvalidArgument
	}
	id, err := s.registry.SetServiceURI(ctx, req.URL)
	if err != nil {
		return TxIntentRef{}, err
	}
	return TxIntentRef{ID: id}, nil
}

// SetAIServiceURI submits an AI service registry setServiceURI txintent.
func (s *Server) SetAIServiceURI(ctx context.Context, req SetAIServiceURIRequest) (TxIntentRef, error) {
	if s.aiRegistry == nil {
		return TxIntentRef{}, ErrUnimplemented
	}
	if req.URL == "" {
		return TxIntentRef{}, ErrInvalidArgument
	}
	id, err := s.aiRegistry.SetServiceURI(ctx, req.URL)
	if err != nil {
		return TxIntentRef{}, err
	}
	return TxIntentRef{ID: id}, nil
}

// GetOnChainServiceURI returns the current on-chain ServiceRegistry pointer.
func (s *Server) GetOnChainServiceURI(ctx context.Context, _ struct{}) (OnChainServiceURIStatus, error) {
	if s.orch == nil {
		return OnChainServiceURIStatus{}, ErrUnimplemented
	}
	uri, err := s.orch.GetOnChainServiceURI(ctx)
	if err != nil {
		return OnChainServiceURIStatus{}, err
	}
	return OnChainServiceURIStatus{URL: uri}, nil
}

// GetOnChainAIServiceURI returns the current on-chain AI service registry pointer.
func (s *Server) GetOnChainAIServiceURI(ctx context.Context, _ struct{}) (OnChainAIServiceURIStatus, error) {
	if s.aiOrch == nil {
		return OnChainAIServiceURIStatus{}, ErrUnimplemented
	}
	uri, err := s.aiOrch.GetOnChainServiceURI(ctx)
	if err != nil {
		return OnChainAIServiceURIStatus{}, err
	}
	return OnChainAIServiceURIStatus{URL: uri}, nil
}

// IsRegistered reports whether the orchestrator has a ServiceRegistry pointer.
func (s *Server) IsRegistered(ctx context.Context, _ struct{}) (RegistrationStatus, error) {
	if s.orch == nil {
		return RegistrationStatus{}, ErrUnimplemented
	}
	registered, err := s.orch.IsRegistered(ctx)
	if err != nil {
		return RegistrationStatus{}, err
	}
	return RegistrationStatus{Registered: registered}, nil
}

// IsAIRegistered reports whether the orchestrator has an AI service registry pointer.
func (s *Server) IsAIRegistered(ctx context.Context, _ struct{}) (AIRegistrationStatus, error) {
	if s.aiOrch == nil {
		return AIRegistrationStatus{}, ErrUnimplemented
	}
	registered, err := s.aiOrch.IsRegistered(ctx)
	if err != nil {
		return AIRegistrationStatus{}, err
	}
	return AIRegistrationStatus{Registered: registered}, nil
}

// GetWalletBalance returns the daemon wallet's current ETH balance.
func (s *Server) GetWalletBalance(ctx context.Context, _ struct{}) (WalletBalanceStatus, error) {
	if s.orch == nil {
		return WalletBalanceStatus{}, ErrUnimplemented
	}
	wallet, bal, err := s.orch.GetWalletBalance(ctx)
	if err != nil {
		return WalletBalanceStatus{}, err
	}
	return WalletBalanceStatus{
		WalletAddress: wallet,
		BalanceWei:    bal,
	}, nil
}

// GetTxIntent returns the snapshot for a TxIntent id.
func (s *Server) GetTxIntent(ctx context.Context, ref TxIntentRef) (TxIntentSnapshot, error) {
	if s.tx == nil {
		return TxIntentSnapshot{}, ErrUnimplemented
	}
	intent, err := s.tx.Status(ctx, txintent.IntentID(ref.ID))
	if err != nil {
		return TxIntentSnapshot{}, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	out := TxIntentSnapshot{
		ID:                    intent.ID,
		Kind:                  intent.Kind,
		Status:                intent.Status.String(),
		CreatedAtUnixNano:     intent.CreatedAt.UnixNano(),
		LastUpdatedAtUnixNano: intent.LastUpdatedAt.UnixNano(),
		AttemptCount:          uint32(len(intent.Attempts)), //nolint:gosec // G115: TxIntent attempt counts are bounded well below uint32 max
	}
	if intent.ConfirmedAt != nil {
		out.ConfirmedAtUnixNano = intent.ConfirmedAt.UnixNano()
	}
	if intent.FailedReason != nil {
		out.FailedClass = intent.FailedReason.Class.String()
		out.FailedCode = intent.FailedReason.Code
		out.FailedMessage = intent.FailedReason.Msg
	}
	return out, nil
}

// StreamRoundEvents pushes Round transitions to the supplied callback
// until ctx is cancelled or the source closes its channel. Used by
// gRPC's server-streaming wrapper as well as in-process tests.
func (s *Server) StreamRoundEvents(ctx context.Context, send func(RoundEvent) error) error {
	if s.rc == nil {
		return ErrUnimplemented
	}
	rounds, err := s.rc.SubscribeRounds(ctx)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case r, ok := <-rounds:
			if !ok {
				return nil
			}
			ev := RoundEvent{
				Number:       uint64(r.Number),
				StartBlock:   uint64(r.StartBlock),
				L1StartBlock: uint64(r.L1StartBlock),
				Length:       uint64(r.Length),
				Initialized:  r.Initialized,
				BlockHash:    r.BlockHash,
			}
			if err := send(ev); err != nil {
				return err
			}
		}
	}
}
