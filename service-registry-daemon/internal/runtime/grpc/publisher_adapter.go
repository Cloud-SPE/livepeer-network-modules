package grpc

import (
	"context"

	registryv1 "github.com/Cloud-SPE/livepeer-network-modules/proto-contracts/livepeer/registry/v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

// publisherAdapter implements registryv1.PublisherServer.
type publisherAdapter struct {
	registryv1.UnimplementedPublisherServer
	srv *Server
}

func newPublisherAdapter(s *Server) *publisherAdapter {
	return &publisherAdapter{srv: s}
}

func (a *publisherAdapter) GetIdentity(ctx context.Context, _ *emptypb.Empty) (*registryv1.IdentityResult, error) {
	addr, err := a.srv.GetIdentity(ctx)
	if err != nil {
		return nil, errorToStatus(err)
	}
	return &registryv1.IdentityResult{EthAddress: string(addr)}, nil
}

func (a *publisherAdapter) Health(ctx context.Context, _ *emptypb.Empty) (*registryv1.HealthResult, error) {
	h := a.srv.Health(ctx)
	return &registryv1.HealthResult{
		Mode:              h.Mode,
		ChainOk:           h.ChainOK,
		ManifestFetcherOk: h.ManifestFetcherOK,
		CacheSize:         int32(h.CacheSize),
		LastChainSuccess:  timeToProto(h.LastChainSuccess),
	}, nil
}
