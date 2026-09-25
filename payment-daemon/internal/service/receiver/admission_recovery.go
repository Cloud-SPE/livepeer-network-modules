package receiver

import (
	"context"
	"crypto/sha256"
	"errors"
	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/identity"
	"strconv"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/store"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const admissionErrorDomain = "payments.livepeer.org"

func admissionFailure(reason, message string) error {
	return admissionStatus(codes.FailedPrecondition, reason, message)
}

func admissionStatus(code codes.Code, reason, message string) error {
	s, err := status.New(code, message).WithDetails(&errdetails.ErrorInfo{Domain: admissionErrorDomain, Reason: reason})
	if err != nil {
		return status.Error(codes.Internal, "encode admission failure")
	}
	return s.Err()
}

func (s *Service) authorizationView(auth *store.WholesaleAuthorization) *pb.GetSpendAuthorizationResponse {
	if auth == nil {
		return nil
	}
	return &pb.GetSpendAuthorizationResponse{WholesaleAccountId: auth.WholesaleAccountID, Payee: auth.Payee, SettlementDomainId: s.settlementDomainID,
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
	if !identity.ValidWholesaleAccountID(req.GetWholesaleAccountId()) || len(req.GetPayer()) != 20 || req.GetAuthorizationId() == "" || len(req.GetAuthorizationFingerprint()) != sha256.Size {
		return nil, status.Error(codes.InvalidArgument, "payer, authorization id and SHA-256 fingerprint required")
	}
	result, canceled, err := s.store.CancelAuthorizationAdmission(req.GetPayer(), s.recipient, req.GetAuthorizationId(), req.GetAuthorizationFingerprint(), req.GetWholesaleAccountId())
	if errors.Is(err, store.ErrAuthorizationFingerprint) {
		return nil, status.Error(codes.InvalidArgument, "authorization_id reused with different content")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "cancel authorization admission: %v", err)
	}
	return &pb.CancelAuthorizationAdmissionResponse{Canceled: canceled, Authorization: s.authorizationView(result.Authorization), Account: s.wholesaleAccountView(result.Account)}, nil
}

// Every admission result has safe correlated diagnostics, including validation
// failures that happen before the store is called. Never log wire bytes or err.Error().
func (s *Service) AdmitAuthorization(ctx context.Context, req *pb.AdmitAuthorizationRequest) (*pb.AdmitAuthorizationResponse, error) {
	result, err := s.admitAuthorization(ctx, req)
	var auth pb.SpendAuthorization
	_ = proto.Unmarshal(req.GetAuthorizationBytes(), &auth)
	p := auth.GetPayload()
	reason := "ADMITTED"
	if err != nil {
		reason = "ADMISSION_OUTCOME_UNKNOWN"
		st := status.Convert(err)
		for _, detail := range st.Details() {
			if info, ok := detail.(*errdetails.ErrorInfo); ok && info.Domain == admissionErrorDomain {
				reason = info.Reason
				info.Metadata = map[string]string{"request_id": diagnosticID(p.GetRequestId()), "authorization_id": diagnosticID(p.GetAuthorizationId()), "predecessor_authorization_id": diagnosticID(p.GetPredecessorAuthorizationId()), "session_id": diagnosticID(p.GetSessionId()), "revision": strconv.FormatUint(p.GetRevision(), 10)}
				// Replace rather than append a second ambiguous ErrorInfo.
				if enriched, e := status.New(st.Code(), st.Message()).WithDetails(info); e == nil {
					err = enriched.Err()
				}
				break
			}
		}
	}
	s.logger.Info("authorization admission decision", "request_id", diagnosticID(p.GetRequestId()), "authorization_id", diagnosticID(p.GetAuthorizationId()), "predecessor_authorization_id", diagnosticID(p.GetPredecessorAuthorizationId()), "session_id", diagnosticID(p.GetSessionId()), "revision", p.GetRevision(), "reason", reason, "grpc_code", status.Code(err).String())
	return result, err
}
func diagnosticID(id string) string {
	if len(id) > 256 {
		return "invalid_length"
	}
	return id
}
func fundingRejectionReason(reason pb.PaymentRejectionReason) string {
	switch reason {
	case pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_INVALID_RECIPIENT_RAND:
		return "FUNDING_INVALID_RECIPIENT_RAND"
	case pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_NONCE_CAP_REACHED:
		return "FUNDING_NONCE_CAP_REACHED"
	case pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_INVALID_SIGNATURE:
		return "FUNDING_INVALID_SIGNATURE"
	default:
		return "FUNDING_TICKETS_REJECTED"
	}
}
