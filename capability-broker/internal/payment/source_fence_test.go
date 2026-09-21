package payment

import (
	"context"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/grpc"
	"strings"
	"testing"
)

type sourceRPC struct {
	*revenueRPC
	account string
}

func (r *sourceRPC) FreezeRevenueSource(context.Context, *pb.FreezeRevenueSourceRequest, ...grpc.CallOption) (*pb.RevenueSourceStatus, error) {
	return &pb.RevenueSourceStatus{SettlementDomainId: r.reportDomain, Frozen: true}, nil
}
func (r *sourceRPC) GetRevenueSourceStatus(context.Context, *pb.GetRevenueSourceStatusRequest, ...grpc.CallOption) (*pb.RevenueSourceStatus, error) {
	return &pb.RevenueSourceStatus{SettlementDomainId: r.reportDomain, Frozen: true}, nil
}
func (r *sourceRPC) CloseUnexecutedAuthorization(context.Context, *pb.CloseUnexecutedAuthorizationRequest, ...grpc.CallOption) (*pb.CloseUnexecutedAuthorizationResponse, error) {
	return &pb.CloseUnexecutedAuthorizationResponse{SettlementDomainId: r.reportDomain, Fenced: true, WholesaleAccountId: r.account}, nil
}
func TestSourceControlAndRecoveryStayBoundThroughMetrics(t *testing.T) {
	domain := "0x" + strings.Repeat("a", 64)
	rpc := &sourceRPC{revenueRPC: &revenueRPC{domain: domain, reportDomain: domain}, account: "test-account"}
	client := WithMetrics(&GRPC{client: rpc, settlementDomainID: domain})
	ctx := context.Background()
	control, ok := client.(SourceControl)
	if !ok {
		t.Fatal("metrics lost source control")
	}
	recovery, ok := client.(UnexecutedRecovery)
	if !ok {
		t.Fatal("metrics lost recovery")
	}
	for _, wrong := range []bool{false, true} {
		if wrong {
			rpc.reportDomain = "0x" + strings.Repeat("b", 64)
		}
		_, freezeErr := control.FreezeRevenueSource(ctx, "retire")
		_, statusErr := control.RevenueSourceStatus(ctx)
		recoverErr := recovery.CloseUnexecutedAuthorization(ctx, make([]byte, 20), "orphan", "no binding", "test-account")
		if wrong && (freezeErr == nil || statusErr == nil || recoverErr == nil) {
			t.Fatal("wrong source accepted")
		}
		if !wrong && (freezeErr != nil || statusErr != nil || recoverErr != nil) {
			t.Fatalf("source adapter %v %v %v", freezeErr, statusErr, recoverErr)
		}
	}
}

func TestUnexecutedRecoveryRejectsAccountDowngrade(t *testing.T) {
	for _, account := range []string{"", "blueclaw-prod"} {
		rpc := &sourceRPC{revenueRPC: &revenueRPC{domain: MockSettlementDomainID, reportDomain: MockSettlementDomainID}, account: account}
		client := WithMetrics(&GRPC{client: rpc, settlementDomainID: MockSettlementDomainID})
		if err := client.(UnexecutedRecovery).CloseUnexecutedAuthorization(context.Background(), make([]byte, 20), "orphan", "no binding", "loc-prod"); err == nil {
			t.Fatalf("accepted account %q", account)
		}
	}
}
