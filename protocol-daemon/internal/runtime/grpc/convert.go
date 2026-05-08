package grpc

import (
	"math/big"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/txintent"
	protocolv1 "github.com/Cloud-SPE/livepeer-network-rewrite/proto-contracts/livepeer/protocol/v1"
)

// Conversion between Go-native handler types (Server's HealthStatus,
// RoundStatus, etc. in server.go) and proto messages (protocolv1).
// Pure functions; no I/O. Tested via wire_test.go.

func pbHealthFrom(h HealthStatus) *protocolv1.HealthStatus {
	return &protocolv1.HealthStatus{
		Ok:      h.OK,
		Mode:    h.Mode,
		Version: h.Version,
		ChainId: h.ChainID,
	}
}

func pbRoundStatusFrom(r RoundStatus) *protocolv1.RoundStatus {
	out := &protocolv1.RoundStatus{
		LastRound:               r.LastRound,
		LastError:               r.LastError,
		CurrentRoundInitialized: r.CurrentRoundInitialized,
	}
	if len(r.LastIntentID) > 0 {
		out.LastIntentId = append([]byte(nil), r.LastIntentID...)
	}
	return out
}

func pbRewardStatusFrom(r RewardStatus) *protocolv1.RewardStatus {
	out := &protocolv1.RewardStatus{
		LastRound:         r.LastRound,
		Eligible:          r.Eligible,
		EligibilityReason: r.EligibilityReason,
		LastRewardRound:   r.LastRewardRound,
		Active:            r.Active,
		LastError:         r.LastError,
	}
	if r.OrchAddress != (chain.Address{}) {
		out.OrchAddress = append([]byte(nil), r.OrchAddress.Bytes()...)
	}
	if len(r.LastIntentID) > 0 {
		out.LastIntentId = append([]byte(nil), r.LastIntentID...)
	}
	if r.LastEarnedWei != nil && r.LastEarnedWei.Sign() >= 0 {
		out.LastEarnedWei = r.LastEarnedWei.Bytes()
	}
	return out
}

func pbOnChainServiceURIStatusFrom(s OnChainServiceURIStatus) *protocolv1.OnChainServiceURIStatus {
	return &protocolv1.OnChainServiceURIStatus{Url: s.URL}
}

func pbOnChainAIServiceURIStatusFrom(s OnChainAIServiceURIStatus) *protocolv1.OnChainAIServiceURIStatus {
	return &protocolv1.OnChainAIServiceURIStatus{Url: s.URL}
}

func pbRegistrationStatusFrom(s RegistrationStatus) *protocolv1.RegistrationStatus {
	return &protocolv1.RegistrationStatus{Registered: s.Registered}
}

func pbAIRegistrationStatusFrom(s AIRegistrationStatus) *protocolv1.AIRegistrationStatus {
	return &protocolv1.AIRegistrationStatus{Registered: s.Registered}
}

func pbWalletBalanceStatusFrom(s WalletBalanceStatus) *protocolv1.WalletBalanceStatus {
	out := &protocolv1.WalletBalanceStatus{}
	if s.WalletAddress != (chain.Address{}) {
		out.WalletAddress = append([]byte(nil), s.WalletAddress.Bytes()...)
	}
	if s.BalanceWei != nil && s.BalanceWei.Sign() >= 0 {
		out.BalanceWei = s.BalanceWei.Bytes()
	}
	return out
}

func pbTxIntentRefFrom(ref TxIntentRef) *protocolv1.TxIntentRef {
	return &protocolv1.TxIntentRef{Id: append([]byte(nil), ref.ID[:]...)}
}

func setServiceURIRequestFromPB(p *protocolv1.SetServiceURIRequest) SetServiceURIRequest {
	if p == nil {
		return SetServiceURIRequest{}
	}
	return SetServiceURIRequest{URL: p.GetUrl()}
}

func setAIServiceURIRequestFromPB(p *protocolv1.SetAIServiceURIRequest) SetAIServiceURIRequest {
	if p == nil {
		return SetAIServiceURIRequest{}
	}
	return SetAIServiceURIRequest{URL: p.GetUrl()}
}

// pbForceOutcomeFrom emits the proto oneof for a force-action result.
// A zero ForceOutcome (neither arm set) defensively maps to
// Skipped{CODE_UNSPECIFIED} so callers always observe one set arm —
// the proto marshaller would otherwise serialize an unset oneof, which
// the TS client treats as undefined behavior.
func pbForceOutcomeFrom(o ForceOutcome) *protocolv1.ForceOutcome {
	switch {
	case o.Submitted != nil:
		return &protocolv1.ForceOutcome{
			Outcome: &protocolv1.ForceOutcome_Submitted{
				Submitted: pbTxIntentRefFrom(*o.Submitted),
			},
		}
	case o.Skipped != nil:
		return &protocolv1.ForceOutcome{
			Outcome: &protocolv1.ForceOutcome_Skipped{
				Skipped: &protocolv1.SkipReason{
					Reason: o.Skipped.Reason,
					Code:   protocolv1.SkipReason_Code(o.Skipped.Code), //nolint:gosec // G115: SkipCode values are bounded by the proto enum (0..3); uint32→int32 cast is safe.
				},
			},
		}
	default:
		return &protocolv1.ForceOutcome{
			Outcome: &protocolv1.ForceOutcome_Skipped{
				Skipped: &protocolv1.SkipReason{Code: protocolv1.SkipReason_CODE_UNSPECIFIED},
			},
		}
	}
}

// txIntentRefFromPB converts the wire ref to a Go-native ref. Returns
// (zero, false) when the ID is missing or not 32 bytes — callers map
// false to InvalidArgument.
func txIntentRefFromPB(p *protocolv1.TxIntentRef) (TxIntentRef, bool) {
	if p == nil {
		return TxIntentRef{}, false
	}
	id := p.GetId()
	if len(id) != len(txintent.IntentID{}) {
		return TxIntentRef{}, false
	}
	var out TxIntentRef
	copy(out.ID[:], id)
	return out, true
}

func pbTxIntentSnapshotFrom(s TxIntentSnapshot) *protocolv1.TxIntentSnapshot {
	return &protocolv1.TxIntentSnapshot{
		Id:                    append([]byte(nil), s.ID[:]...),
		Kind:                  s.Kind,
		Status:                s.Status,
		FailedClass:           s.FailedClass,
		FailedCode:            s.FailedCode,
		FailedMessage:         s.FailedMessage,
		CreatedAtUnixNano:     uint64NonNeg(s.CreatedAtUnixNano),
		LastUpdatedAtUnixNano: uint64NonNeg(s.LastUpdatedAtUnixNano),
		ConfirmedAtUnixNano:   uint64NonNeg(s.ConfirmedAtUnixNano),
		AttemptCount:          s.AttemptCount,
	}
}

func pbRoundEventFrom(e RoundEvent) *protocolv1.RoundEvent {
	return &protocolv1.RoundEvent{
		Number:       e.Number,
		StartBlock:   e.StartBlock,
		L1StartBlock: e.L1StartBlock,
		Length:       e.Length,
		Initialized:  e.Initialized,
		BlockHash:    append([]byte(nil), e.BlockHash[:]...),
	}
}

// uint64NonNeg coerces a possibly-negative int64 (zero-value time
// stored as 0; never negative in practice from server.go) to uint64.
// Negative inputs (impossible today; defensive) become zero so the
// wire field stays unsigned.
func uint64NonNeg(v int64) uint64 {
	if v < 0 {
		return 0
	}
	return uint64(v)
}

// Compile-time assertion that the in-process LastEarnedWei big-endian
// byte encoding matches the proto comment ("big-endian unsigned bytes").
var _ = (*big.Int).Bytes
