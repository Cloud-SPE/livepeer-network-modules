package payment

import (
	"context"
	"fmt"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

// RevenueReader intentionally exposes no receiver mutation operation.
type RevenueReader interface {
	RoundRevenue(context.Context, int64) (*pb.GetRoundRevenueResponse, error)
}

func (g *GRPC) RoundRevenue(ctx context.Context, round int64) (*pb.GetRoundRevenueResponse, error) {
	domain, err := g.SettlementDomain(ctx)
	if err != nil {
		return nil, err
	}
	result, err := g.client.GetRoundRevenue(ctx, &pb.GetRoundRevenueRequest{RoundId: round})
	if err != nil {
		return nil, err
	}
	if result.RoundId != round || result.SettlementDomainId != domain {
		return nil, fmt.Errorf("receiver revenue source identity changed")
	}
	return result, nil
}

func (m *metered) RoundRevenue(ctx context.Context, round int64) (*pb.GetRoundRevenueResponse, error) {
	reader, ok := m.inner.(RevenueReader)
	if !ok {
		return nil, fmt.Errorf("receiver revenue reporting unavailable")
	}
	return reader.RoundRevenue(ctx, round)
}
