package receiver

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/store"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const admissionErrorDomain = "payments.livepeer.org"

func admissionFailure(reason, message string) error {
	s, err := status.New(codes.FailedPrecondition, message).WithDetails(&errdetails.ErrorInfo{Domain: admissionErrorDomain, Reason: reason})
	if err != nil {
		return status.Error(codes.Internal, "encode admission failure")
	}
	return s.Err()
}

func authorizationView(auth *store.WholesaleAuthorization) *pb.GetSpendAuthorizationResponse {
	if auth == nil {
		return nil
	}
	return &pb.GetSpendAuthorizationResponse{
		State: authorizationState(auth.State), ReservedValueWei: &pb.BigUInt{Value: decimalBytes(auth.ReservedWei)},
		BilledValueWei: &pb.BigUInt{Value: decimalBytes(auth.BilledWei)}, ReleasedValueWei: &pb.BigUInt{Value: decimalBytes(auth.ReleasedWei)},
		ActualUnits: auth.ActualUnits, SettlementSeq: auth.SettlementSeq, ObservedAt: auth.UpdatedAt.Format(time.RFC3339Nano),
	}
}

// Cancellation is a reconciliation operation, also allowed while admission is
// frozen. It never undoes an accepted authorization or bills/releases usage.
func (s *Service) CancelAuthorizationAdmission(_ context.Context, req *pb.CancelAuthorizationAdmissionRequest) (*pb.CancelAuthorizationAdmissionResponse, error) {
	if s.settlementDomainErr != nil || req.GetSettlementDomainId() == "" || req.GetSettlementDomainId() != s.settlementDomainID {
		return nil, status.Error(codes.PermissionDenied, "settlement domain does not match receiver ledger")
	}
	if len(req.GetPayer()) != 20 || req.GetAuthorizationId() == "" || len(req.GetAuthorizationFingerprint()) != sha256.Size {
		return nil, status.Error(codes.InvalidArgument, "payer, authorization id and SHA-256 fingerprint required")
	}
	result, canceled, err := s.store.CancelAuthorizationAdmission(req.GetPayer(), s.recipient, req.GetAuthorizationId(), req.GetAuthorizationFingerprint())
	if errors.Is(err, store.ErrAuthorizationFingerprint) {
		return nil, status.Error(codes.InvalidArgument, "authorization_id reused with different content")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "cancel authorization admission: %v", err)
	}
	return &pb.CancelAuthorizationAdmissionResponse{Canceled: canceled, Authorization: authorizationView(result.Authorization), Account: s.wholesaleAccountView(result.Account)}, nil
}
