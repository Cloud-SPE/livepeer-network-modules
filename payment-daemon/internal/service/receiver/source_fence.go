package receiver

import (
	"context"
	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/identity"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Service) requireUnfrozenSource() error {
	fence, err := s.store.SourceFence()
	if err != nil {
		return status.Error(codes.Internal, "source fence unavailable")
	}
	if fence.Frozen {
		return admissionFailure("RECEIVER_SOURCE_FROZEN", "receiver source permanently frozen for retirement")
	}
	return nil
}
func (s *Service) FreezeRevenueSource(ctx context.Context, req *pb.FreezeRevenueSourceRequest) (*pb.RevenueSourceStatus, error) {
	if s.settlementDomainErr != nil || req.GetSettlementDomainId() == "" || req.GetSettlementDomainId() != s.settlementDomainID {
		return nil, status.Error(codes.PermissionDenied, "receiver source identity mismatch")
	}
	s.sourceGate.Lock()
	_, err := s.store.FreezeSource(req.GetReason())
	s.sourceGate.Unlock()
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "source cannot freeze: %v", err)
	}
	return s.GetRevenueSourceStatus(ctx, &pb.GetRevenueSourceStatusRequest{SettlementDomainId: s.settlementDomainID})
}
func (s *Service) GetRevenueSourceStatus(ctx context.Context, req *pb.GetRevenueSourceStatusRequest) (*pb.RevenueSourceStatus, error) {
	if s.settlementDomainErr != nil || req.GetSettlementDomainId() == "" || req.GetSettlementDomainId() != s.settlementDomainID {
		return nil, status.Error(codes.PermissionDenied, "receiver source identity mismatch")
	}
	result := &pb.RevenueSourceStatus{SettlementDomainId: s.settlementDomainID, ChainId: s.chainID, Payee: append([]byte(nil), s.recipient...)}
	fence, err := s.store.SourceFence()
	if err != nil {
		return nil, status.Error(codes.Internal, "source fence unavailable")
	}
	result.Frozen = fence.Frozen
	result.FreezeReason = fence.Reason
	if fence.Frozen {
		result.FrozenAt = fence.FrozenAt.Format(time.RFC3339Nano)
	}
	result.ActiveAuthorizations, err = s.store.ActiveAuthorizations()
	if err != nil {
		return nil, status.Error(codes.Internal, "authorization inventory unavailable")
	}
	pending, redeemed, legacy, err := s.store.RedemptionHistory()
	if err != nil {
		return nil, status.Error(codes.Internal, "redemption inventory unavailable")
	}
	result.PendingRedemptions = uint64(len(pending))
	hold := func(reason string) (*pb.RevenueSourceStatus, error) {
		result.IncompleteReason = reason
		return result, nil
	}
	if !result.Frozen || result.ActiveAuthorizations != 0 || result.PendingRedemptions != 0 {
		return hold("source not frozen or settlement remains")
	}
	if legacy {
		return hold("legacy redemption history lacks evidence")
	}
	for _, record := range redeemed {
		if record.ConfirmedOnChain {
			if record.Inclusion == nil {
				return hold("confirmed redemption lacks inclusion evidence")
			}
			if record.Inclusion.Round > result.LastRedemptionRound {
				result.LastRedemptionRound = record.Inclusion.Round
			}
		} else {
			switch record.DrainReason {
			case "expired", "face_value_too_low", "reverted":
			default:
				return hold("unresolved historical redemption")
			}
		}
	}
	if s.RevenueReporter == nil {
		return hold("confirmed chain reporting unavailable")
	}
	clock, err := s.GetRoundRevenue(ctx, &pb.GetRoundRevenueRequest{RoundId: 0})
	if err != nil {
		return nil, err
	}
	if clock.CompleteThroughRound < result.LastRedemptionRound {
		return hold("latest redemption round not yet complete")
	}
	proof, err := s.GetRoundRevenue(ctx, &pb.GetRoundRevenueRequest{RoundId: clock.CompleteThroughRound})
	if err != nil {
		return nil, err
	}
	result.CompleteThroughRound = proof.CompleteThroughRound
	result.ObservedAt = proof.ObservedAt
	if !proof.Complete {
		return hold(proof.IncompleteReason)
	}
	result.Complete = true
	return result, nil
}

func (s *Service) CloseUnexecutedAuthorization(_ context.Context, req *pb.CloseUnexecutedAuthorizationRequest) (*pb.CloseUnexecutedAuthorizationResponse, error) {
	if !identity.ValidWholesaleAccountID(req.GetWholesaleAccountId()) {
		return nil, status.Error(codes.InvalidArgument, "wholesale_account_id is required")
	}
	if s.settlementDomainErr != nil || req.GetSettlementDomainId() == "" || req.GetSettlementDomainId() != s.settlementDomainID {
		return nil, status.Error(codes.PermissionDenied, "receiver source identity mismatch")
	}
	s.sourceGate.RLock()
	defer s.sourceGate.RUnlock()
	if err := s.store.CloseUnexecutedAuthorization(req.GetPayer(), s.recipient, req.GetAuthorizationId(), req.GetReason(), req.GetWholesaleAccountId()); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "unexecuted recovery held: %v", err)
	}
	s.recordWholesaleTotals()
	return &pb.CloseUnexecutedAuthorizationResponse{WholesaleAccountId: req.GetWholesaleAccountId(), SettlementDomainId: s.settlementDomainID, Fenced: true}, nil
}
