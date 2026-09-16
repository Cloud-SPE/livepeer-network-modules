package payment

import (
	"context"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/grpc"
	"strings"
	"testing"
)

type revenueRPC struct {
	pb.PayeeDaemonClient
	domain       string
	reportDomain string
}

func (r *revenueRPC) Health(context.Context, *pb.HealthRequest, ...grpc.CallOption) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{SettlementDomainId: r.domain}, nil
}
func (r *revenueRPC) GetRoundRevenue(_ context.Context, req *pb.GetRoundRevenueRequest, _ ...grpc.CallOption) (*pb.GetRoundRevenueResponse, error) {
	return &pb.GetRoundRevenueResponse{RoundId: req.RoundId, SettlementDomainId: r.reportDomain, Complete: true}, nil
}

func TestRevenueAdapterSurvivesMetricsAndRejectsLedgerReplacement(t *testing.T) {
	domain := "0x" + strings.Repeat("a", 64)
	rpc := &revenueRPC{domain: domain, reportDomain: domain}
	client := WithMetrics(&GRPC{client: rpc, settlementDomainID: domain})
	reader, ok := client.(RevenueReader)
	if !ok {
		t.Fatal("metrics wrapper lost read-only reporting")
	}
	if _, err := reader.RoundRevenue(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	rpc.reportDomain = "0x" + strings.Repeat("b", 64)
	if _, err := reader.RoundRevenue(context.Background(), 100); err == nil {
		t.Fatal("mismatched revenue ledger accepted")
	}
	rpc.domain = rpc.reportDomain
	if _, err := reader.RoundRevenue(context.Background(), 100); err == nil {
		t.Fatal("replaced local receiver accepted")
	}
}
