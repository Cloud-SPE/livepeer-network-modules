package receiver_test

import (
	"context"
	"crypto/sha256"
	"testing"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCancelAuthorizationAdmissionRPC(t *testing.T) {
	client, _, cleanup := stand(t)
	defer cleanup()
	fp := sha256.Sum256([]byte("authorization"))
	req := &pb.CancelAuthorizationAdmissionRequest{SettlementDomainId: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Payer: bytes20(1), AuthorizationId: "revision", AuthorizationFingerprint: fp[:]}
	for i := 0; i < 2; i++ {
		result, err := client.CancelAuthorizationAdmission(context.Background(), req)
		if err != nil || !result.GetCanceled() || result.GetAuthorization() != nil {
			t.Fatalf("cancellation %+v %v", result, err)
		}
	}
	state, err := client.GetSpendAuthorization(context.Background(), &pb.GetSpendAuthorizationRequest{SettlementDomainId: req.SettlementDomainId, Payer: req.Payer, AuthorizationId: req.AuthorizationId})
	if err != nil || state.GetState() != pb.SpendAuthorizationState_SPEND_AUTHORIZATION_CANCELED_UNUSED || state.GetActualUnits() != 0 || state.GetObservedAt() == "" {
		t.Fatalf("canceled state is not queryable: %+v %v", state, err)
	}
	req.AuthorizationFingerprint = []byte("invalid")
	if _, err := client.CancelAuthorizationAdmission(context.Background(), req); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid fingerprint: %v", err)
	}
	different := sha256.Sum256([]byte("different"))
	req.AuthorizationFingerprint = different[:]
	if _, err := client.CancelAuthorizationAdmission(context.Background(), req); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("changed fingerprint: %v", err)
	}
	req.SettlementDomainId = "foreign"
	if _, err := client.CancelAuthorizationAdmission(context.Background(), req); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("foreign domain: %v", err)
	}
}
