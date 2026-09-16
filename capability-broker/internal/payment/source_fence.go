package payment

import (
	"context"
	"fmt"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

type SourceControl interface {
	FreezeRevenueSource(context.Context, string) (*pb.RevenueSourceStatus, error)
	RevenueSourceStatus(context.Context) (*pb.RevenueSourceStatus, error)
}

func (g *GRPC) FreezeRevenueSource(ctx context.Context, reason string) (*pb.RevenueSourceStatus, error) {
	domain, err := g.SettlementDomain(ctx)
	if err != nil {
		return nil, err
	}
	result, err := g.client.FreezeRevenueSource(ctx, &pb.FreezeRevenueSourceRequest{SettlementDomainId: domain, Reason: reason})
	if err != nil {
		return nil, err
	}
	if result == nil || result.SettlementDomainId != domain || !result.Frozen {
		return nil, fmt.Errorf("receiver freeze source proof invalid")
	}
	return result, nil
}
func (g *GRPC) RevenueSourceStatus(ctx context.Context) (*pb.RevenueSourceStatus, error) {
	domain, err := g.SettlementDomain(ctx)
	if err != nil {
		return nil, err
	}
	result, err := g.client.GetRevenueSourceStatus(ctx, &pb.GetRevenueSourceStatusRequest{SettlementDomainId: domain})
	if err != nil {
		return nil, err
	}
	if result == nil || result.SettlementDomainId != domain {
		return nil, fmt.Errorf("receiver source status identity invalid")
	}
	return result, nil
}
func (m *metered) FreezeRevenueSource(ctx context.Context, reason string) (*pb.RevenueSourceStatus, error) {
	inner, ok := m.inner.(SourceControl)
	if !ok {
		return nil, fmt.Errorf("receiver source control unavailable")
	}
	return inner.FreezeRevenueSource(ctx, reason)
}
func (m *metered) RevenueSourceStatus(ctx context.Context) (*pb.RevenueSourceStatus, error) {
	inner, ok := m.inner.(SourceControl)
	if !ok {
		return nil, fmt.Errorf("receiver source control unavailable")
	}
	return inner.RevenueSourceStatus(ctx)
}

type UnexecutedRecovery interface {
	CloseUnexecutedAuthorization(context.Context, []byte, string, string) error
}

func (g *GRPC) CloseUnexecutedAuthorization(ctx context.Context, payer []byte, id, reason string) error {
	domain, err := g.SettlementDomain(ctx)
	if err != nil {
		return err
	}
	result, err := g.client.CloseUnexecutedAuthorization(ctx, &pb.CloseUnexecutedAuthorizationRequest{SettlementDomainId: domain, Payer: payer, AuthorizationId: id, Reason: reason})
	if err != nil {
		return err
	}
	if result == nil || !result.Fenced || result.SettlementDomainId != domain {
		return fmt.Errorf("unexecuted recovery source proof invalid")
	}
	return nil
}
func (m *metered) CloseUnexecutedAuthorization(ctx context.Context, payer []byte, id, reason string) error {
	inner, ok := m.inner.(UnexecutedRecovery)
	if !ok {
		return fmt.Errorf("receiver unexecuted recovery unavailable")
	}
	return inner.CloseUnexecutedAuthorization(ctx, payer, id, reason)
}
