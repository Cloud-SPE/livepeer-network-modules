package grpc

import (
	"context"
	"encoding/json"

	registryv1 "github.com/Cloud-SPE/livepeer-network-rewrite/proto-contracts/livepeer/registry/v1"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/service/publisher"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
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

func (a *publisherAdapter) BuildManifest(ctx context.Context, req *registryv1.BuildManifestRequest) (*registryv1.BuildResult, error) {
	spec := publisher.BuildSpec{
		EthAddress: types.EthAddress(req.GetProposedEthAddress()),
		Nodes:      nodesFromProto(req.GetProposedNodes()),
	}
	m, err := a.srv.BuildManifest(ctx, spec)
	if err != nil {
		return nil, errorToStatus(err)
	}
	body, jerr := json.Marshal(m)
	if jerr != nil {
		return nil, errorToStatus(jerr)
	}
	canonical, cerr := types.CanonicalBytes(m)
	if cerr != nil {
		return nil, errorToStatus(cerr)
	}
	return &registryv1.BuildResult{
		ManifestJson:    body,
		CanonicalBytes:  canonical,
		CanonicalSha256: types.CanonicalSHA256(canonical),
	}, nil
}

func (a *publisherAdapter) SignManifest(ctx context.Context, req *registryv1.SignManifestRequest) (*registryv1.SignedManifest, error) {
	m, err := types.DecodeUnsignedManifest(req.GetManifestJson())
	if err != nil {
		return nil, errorToStatus(err)
	}
	signed, err := a.srv.SignManifest(ctx, m)
	if err != nil {
		return nil, errorToStatus(err)
	}
	body, jerr := json.Marshal(signed)
	if jerr != nil {
		return nil, errorToStatus(jerr)
	}
	return &registryv1.SignedManifest{
		ManifestJson:   body,
		SignatureValue: signed.Signature.Value,
	}, nil
}

func (a *publisherAdapter) BuildAndSign(ctx context.Context, req *registryv1.BuildAndSignRequest) (*registryv1.SignedManifest, error) {
	unsigned, err := types.DecodeUnsignedManifest(req.GetManifestJson())
	if err != nil {
		return nil, errorToStatus(err)
	}
	signed, err := a.srv.SignManifest(ctx, unsigned)
	if err != nil {
		return nil, errorToStatus(err)
	}
	body, jerr := json.Marshal(signed)
	if jerr != nil {
		return nil, errorToStatus(jerr)
	}
	return &registryv1.SignedManifest{
		ManifestJson:   body,
		SignatureValue: signed.Signature.Value,
	}, nil
}

func (a *publisherAdapter) ProbeWorker(ctx context.Context, req *registryv1.ProbeWorkerRequest) (*registryv1.ProbeResult, error) {
	// Probe is not yet implemented as a Server method (the publisher
	// service has no fetcher dependency yet). Return Unimplemented so
	// callers know to upgrade rather than silently accept empty data.
	_ = ctx
	_ = req
	return nil, errorToStatus(types.ErrChainWriteNotImpl)
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
