package grpc

import (
	"context"

	registryv1 "github.com/Cloud-SPE/livepeer-network-rewrite/proto-contracts/livepeer/registry/v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

// resolverAdapter implements registryv1.ResolverServer by delegating to
// the Go-native Server methods. Each method handles three concerns:
//  1. Translate request proto → Go-native request shape.
//  2. Invoke the Server method.
//  3. Translate Go return value → response proto, mapping errors to
//     gRPC statuses with stable code details.
type resolverAdapter struct {
	registryv1.UnimplementedResolverServer
	srv *Server
}

// newResolverAdapter constructs the adapter. The caller must guarantee
// srv was built with a non-nil Resolver service.
func newResolverAdapter(s *Server) *resolverAdapter {
	return &resolverAdapter{srv: s}
}

func (a *resolverAdapter) ResolveByAddress(ctx context.Context, req *registryv1.ResolveByAddressRequest) (*registryv1.ResolveResult, error) {
	res, err := a.srv.ResolveByAddress(ctx, ResolveByAddressRequest{
		EthAddress:          req.GetEthAddress(),
		AllowLegacyFallback: req.GetAllowLegacyFallback(),
		AllowUnsigned:       req.GetAllowUnsigned(),
		ForceRefresh:        req.GetForceRefresh(),
	})
	if err != nil {
		return nil, errorToStatus(err)
	}
	return resolveResultToProto(res), nil
}

func (a *resolverAdapter) Select(ctx context.Context, req *registryv1.SelectRequest) (*registryv1.SelectResult, error) {
	route, err := a.srv.Select(ctx, SelectRequest{
		Capability: req.GetCapability(),
		Offering:   req.GetOffering(),
		Tier:       req.GetTier(),
		MinWeight:  int(req.GetMinWeight()),
	})
	if err != nil {
		return nil, errorToStatus(err)
	}
	return &registryv1.SelectResult{Route: selectedRouteToProto(route)}, nil
}

func (a *resolverAdapter) ListKnown(ctx context.Context, _ *registryv1.ListKnownRequest) (*registryv1.ListKnownResult, error) {
	entries, err := a.srv.ListKnown(ctx)
	if err != nil {
		return nil, errorToStatus(err)
	}
	out := &registryv1.ListKnownResult{Entries: make([]*registryv1.KnownEntry, 0, len(entries))}
	for _, e := range entries {
		out.Entries = append(out.Entries, &registryv1.KnownEntry{
			EthAddress:      string(e.EthAddress),
			Mode:            resolveModeToProto(e.Mode),
			FreshnessStatus: registryv1.FreshnessStatus_FRESHNESS_STATUS_UNSPECIFIED,
			CachedAt:        timeToProto(e.CachedAt),
		})
	}
	return out, nil
}

func (a *resolverAdapter) Refresh(ctx context.Context, req *registryv1.RefreshRequest) (*emptypb.Empty, error) {
	if err := a.srv.Refresh(ctx, RefreshRequest{
		EthAddress: req.GetEthAddress(),
		Force:      req.GetForce(),
	}); err != nil {
		return nil, errorToStatus(err)
	}
	return &emptypb.Empty{}, nil
}

func (a *resolverAdapter) GetAuditLog(ctx context.Context, req *registryv1.GetAuditLogRequest) (*registryv1.AuditLogResult, error) {
	events, err := a.srv.GetAuditLog(ctx, GetAuditLogRequest{
		EthAddress: req.GetEthAddress(),
		Since:      timeFromProto(req.GetSince()),
		Limit:      req.GetLimit(),
	})
	if err != nil {
		return nil, errorToStatus(err)
	}
	out := &registryv1.AuditLogResult{Events: make([]*registryv1.AuditEvent, 0, len(events))}
	for _, e := range events {
		out.Events = append(out.Events, auditEventToProto(e))
	}
	return out, nil
}

func (a *resolverAdapter) Health(ctx context.Context, _ *emptypb.Empty) (*registryv1.HealthResult, error) {
	h := a.srv.Health(ctx)
	return &registryv1.HealthResult{
		Mode:              h.Mode,
		ChainOk:           h.ChainOK,
		ManifestFetcherOk: h.ManifestFetcherOK,
		CacheSize:         int32(h.CacheSize),
		LastChainSuccess:  timeToProto(h.LastChainSuccess),
	}, nil
}
