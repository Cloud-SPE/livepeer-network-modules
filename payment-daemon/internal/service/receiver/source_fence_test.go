package receiver_test

import (
	"context"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"
)

func TestFrozenReceiverRejectsEveryNewFinancialIntake(t *testing.T) {
	client, _, cleanup := stand(t)
	defer cleanup()
	ctx := context.Background()
	domain := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := client.FreezeRevenueSource(ctx, &pb.FreezeRevenueSourceRequest{SettlementDomainId: "other", Reason: "retire"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("foreign source freeze %v", err)
	}
	result, err := client.FreezeRevenueSource(ctx, &pb.FreezeRevenueSourceRequest{SettlementDomainId: domain, Reason: "settled source retirement"})
	if err != nil || !result.Frozen || result.Complete {
		t.Fatalf("frozen source without real chain reporter %+v %v", result, err)
	}
	for name, call := range map[string]func() error{
		"admit": func() error { _, err := client.AdmitAuthorization(ctx, &pb.AdmitAuthorizationRequest{}); return err },
		"advance": func() error {
			_, err := client.AdvanceAuthorization(ctx, &pb.AdvanceAuthorizationRequest{WholesaleAccountId: "test-account"})
			return err
		},
		"fund": func() error {
			_, err := client.FundWholesaleAccount(ctx, &pb.FundWholesaleAccountRequest{WholesaleAccountId: "test-account"})
			return err
		},
		"open": func() error { _, err := client.OpenSession(ctx, &pb.OpenSessionRequest{}); return err },
		"payment": func() error {
			_, err := client.ProcessPayment(ctx, &pb.ProcessPaymentRequest{})
			return err
		},
		"ticket-params": func() error {
			_, err := client.GetTicketParams(ctx, &pb.GetTicketParamsRequest{WholesaleAccountId: "test-account", TicketStreamId: "test-stream"})
			return err
		},
		"debit": func() error { _, err := client.DebitBalance(ctx, &pb.DebitBalanceRequest{}); return err },
		"close": func() error { _, err := client.CloseSession(ctx, &pb.CloseSessionRequest{}); return err },
	} {
		if err := call(); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("%s admitted after freeze: %v", name, err)
		}
	}
	report, err := client.GetRevenueSourceStatus(ctx, &pb.GetRevenueSourceStatusRequest{SettlementDomainId: domain})
	if err != nil || !report.Frozen || report.FreezeReason != "settled source retirement" {
		t.Fatalf("fence evidence %+v %v", report, err)
	}
}

func TestUnexecutedRecoveryRPCFencesLateAdmissions(t *testing.T) {
	client, _, cleanup := stand(t)
	defer cleanup()
	ctx := context.Background()
	domain := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	request := &pb.CloseUnexecutedAuthorizationRequest{WholesaleAccountId: "test-account", SettlementDomainId: domain, Payer: bytes20(1), AuthorizationId: "lost-admission", Reason: "broker durable no-binding evidence"}
	for i := 0; i < 2; i++ {
		result, err := client.CloseUnexecutedAuthorization(ctx, request)
		if err != nil || !result.Fenced || result.SettlementDomainId != domain {
			t.Fatalf("recovery %+v %v", result, err)
		}
	}
	request.SettlementDomainId = "other"
	if _, err := client.CloseUnexecutedAuthorization(ctx, request); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("foreign orphan recovery %v", err)
	}
}
