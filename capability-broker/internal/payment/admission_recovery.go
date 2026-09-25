package payment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/identity"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// AdmissionRecovery is a trusted receiver operation, independent of gates on
// new admission. Missing support leaves uncertain financial intents pending.
type AdmissionRecovery interface {
	CancelAuthorizationAdmission(context.Context, []byte) (*CanceledAdmission, error)
}

type CanceledAdmission struct {
	Canceled      bool
	Authorization *SpendAuthorizationStatus
	Account       *WholesaleAccount
}

func (g *GRPC) CancelAuthorizationAdmission(ctx context.Context, wire []byte) (*CanceledAdmission, error) {
	domain, err := g.SettlementDomain(ctx)
	if err != nil {
		return nil, err
	}
	var auth pb.SpendAuthorization
	if err := proto.Unmarshal(wire, &auth); err != nil {
		return nil, err
	}
	if !identity.ValidWholesaleAccountID(auth.GetPayload().GetWholesaleAccountId()) || auth.GetPayload().GetSettlementDomainId() != domain {
		return nil, fmt.Errorf("authorization account or domain mismatch")
	}
	fingerprint := sha256.Sum256(wire)
	r, err := g.client.CancelAuthorizationAdmission(ctx, &pb.CancelAuthorizationAdmissionRequest{WholesaleAccountId: auth.GetPayload().GetWholesaleAccountId(), SettlementDomainId: domain, Payer: auth.GetPayload().GetPayer(), AuthorizationId: auth.GetPayload().GetAuthorizationId(), AuthorizationFingerprint: fingerprint[:]})
	if err != nil {
		return nil, err
	}
	if err := g.validateAccount(r.GetAccount(), auth.GetPayload().GetPayer(), auth.GetPayload().GetWholesaleAccountId()); err != nil {
		return nil, err
	}
	if a := r.GetAuthorization(); a != nil && (a.GetWholesaleAccountId() != auth.GetPayload().GetWholesaleAccountId() || a.GetSettlementDomainId() != domain || !bytes.Equal(a.GetPayee(), auth.GetPayload().GetPayee())) {
		return nil, fmt.Errorf("authorization recovery account mismatch")
	}
	return &CanceledAdmission{Canceled: r.GetCanceled(), Authorization: authorizationStatusFromProto(r.GetAuthorization()), Account: accountFromProto(r.GetAccount())}, nil
}

func (m *metered) CancelAuthorizationAdmission(ctx context.Context, wire []byte) (*CanceledAdmission, error) {
	inner, ok := m.inner.(AdmissionRecovery)
	if !ok {
		return nil, errors.ErrUnsupported
	}
	return inner.CancelAuthorizationAdmission(ctx, wire)
}

// AdmissionFailureReason is the receiver's stable reason, never an internal
// error string. Older receivers can still be reconciled by status and fencing.
func AdmissionFailureReason(err error) string {
	for _, detail := range status.Convert(err).Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok && info.Domain == "payments.livepeer.org" {
			return info.Reason
		}
	}
	return ""
}

func AdmissionRefused(err error) bool {
	if errors.Is(err, ErrAdmissionStopped) {
		return true
	}
	_, refused := knownAdmissionReason(AdmissionFailureReason(err))
	return refused && (status.Code(err) == codes.FailedPrecondition || status.Code(err) == codes.InvalidArgument || status.Code(err) == codes.PermissionDenied)
}

// Unknown reasons are never promoted to cancellation by their gRPC code alone.
// The same allowlist bounds public diagnostics and metric labels.
func knownAdmissionReason(reason string) (known, refused bool) {
	switch reason {
	case "AUTHORIZATION_NOT_ACTIVE":
		return true, false
	case "RESERVATION_EXCEEDS_REMAINING_DEBIT", "REVISION_PREDECESSOR_INVALID", "REVISION_LIMITS_BELOW_USAGE", "AUTHORIZATION_EXPIRED_UNUSED", "AUTHORIZATION_ADMISSION_CANCELED", "AUTHORIZATION_NOT_ADMITTED", "AUTHORIZATION_STATE_INVALID", "INSUFFICIENT_WHOLESALE_CREDIT",
		"AUTHORIZATION_MALFORMED", "AUTHORIZATION_SIGNATURE_INVALID", "SETTLEMENT_DOMAIN_MISMATCH", "PAYEE_MISMATCH", "CHAIN_OR_DENOMINATION_MISMATCH", "AUTHORIZATION_IDENTITY_INVALID", "AUTHORIZATION_REQUEST_BINDING_INVALID", "CALLER_KEY_INVALID", "REVISION_FIELDS_INVALID", "PROTOCOL_UNSUPPORTED", "PRICE_SCOPE_MISMATCH", "PRICE_INVALID", "AUTHORIZATION_TIME_INVALID", "AUTHORIZATION_LIMITS_INVALID", "DEBIT_CAP_BELOW_PRICE", "AUTHORIZATION_ID_REUSED", "FUNDING_MALFORMED", "FUNDING_PAYER_MISMATCH", "FUNDING_TICKETS_REJECTED", "FUNDING_VALIDATION_FAILED", "FUNDING_INVALID_RECIPIENT_RAND", "FUNDING_NONCE_CAP_REACHED", "FUNDING_INVALID_SIGNATURE", "RECEIVER_SOURCE_FROZEN":
		return true, true
	default:
		return false, false
	}
}

func SafeAdmissionReason(err error) string {
	if errors.Is(err, ErrAdmissionStopped) {
		return "ADMISSION_STOPPED"
	}
	reason := AdmissionFailureReason(err)
	if known, _ := knownAdmissionReason(reason); known {
		return reason
	}
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled:
		return "RECEIVER_UNAVAILABLE"
	default:
		return "ADMISSION_OUTCOME_UNKNOWN"
	}
}
