package grpc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	protocolv1 "github.com/Cloud-SPE/livepeer-network-rewrite/proto-contracts/livepeer/protocol/v1"
)

// adapter implements protocolv1.ProtocolDaemonServer by delegating to
// the in-process *Server (server.go) and translating types via convert.go.
//
// Error mapping:
//   - ErrUnimplemented → codes.Unimplemented
//   - ErrNotFound      → codes.NotFound
//   - any other        → codes.Internal (the wrapped error message rides on the status)
type adapter struct {
	protocolv1.UnimplementedProtocolDaemonServer
	srv *Server
}

func newAdapter(s *Server) *adapter { return &adapter{srv: s} }

func (a *adapter) Health(ctx context.Context, _ *protocolv1.Empty) (*protocolv1.HealthStatus, error) {
	h, err := a.srv.Health(ctx, struct{}{})
	if err != nil {
		return nil, errorToStatus(err)
	}
	return pbHealthFrom(h), nil
}

func (a *adapter) GetRoundStatus(ctx context.Context, _ *protocolv1.Empty) (*protocolv1.RoundStatus, error) {
	r, err := a.srv.GetRoundStatus(ctx, struct{}{})
	if err != nil {
		return nil, errorToStatus(err)
	}
	return pbRoundStatusFrom(r), nil
}

func (a *adapter) GetRewardStatus(ctx context.Context, _ *protocolv1.Empty) (*protocolv1.RewardStatus, error) {
	r, err := a.srv.GetRewardStatus(ctx, struct{}{})
	if err != nil {
		return nil, errorToStatus(err)
	}
	return pbRewardStatusFrom(r), nil
}

func (a *adapter) ForceInitializeRound(ctx context.Context, _ *protocolv1.Empty) (*protocolv1.ForceOutcome, error) {
	out, err := a.srv.ForceInitializeRound(ctx, struct{}{})
	if err != nil {
		return nil, errorToStatus(err)
	}
	return pbForceOutcomeFrom(out), nil
}

func (a *adapter) ForceRewardCall(ctx context.Context, _ *protocolv1.Empty) (*protocolv1.ForceOutcome, error) {
	out, err := a.srv.ForceRewardCall(ctx, struct{}{})
	if err != nil {
		return nil, errorToStatus(err)
	}
	return pbForceOutcomeFrom(out), nil
}

func (a *adapter) SetServiceURI(ctx context.Context, req *protocolv1.SetServiceURIRequest) (*protocolv1.TxIntentRef, error) {
	out, err := a.srv.SetServiceURI(ctx, setServiceURIRequestFromPB(req))
	if err != nil {
		return nil, errorToStatus(err)
	}
	return pbTxIntentRefFrom(out), nil
}

func (a *adapter) SetAIServiceURI(ctx context.Context, req *protocolv1.SetAIServiceURIRequest) (*protocolv1.TxIntentRef, error) {
	out, err := a.srv.SetAIServiceURI(ctx, setAIServiceURIRequestFromPB(req))
	if err != nil {
		return nil, errorToStatus(err)
	}
	return pbTxIntentRefFrom(out), nil
}

func (a *adapter) GetOnChainServiceURI(ctx context.Context, _ *protocolv1.Empty) (*protocolv1.OnChainServiceURIStatus, error) {
	out, err := a.srv.GetOnChainServiceURI(ctx, struct{}{})
	if err != nil {
		return nil, errorToStatus(err)
	}
	return pbOnChainServiceURIStatusFrom(out), nil
}

func (a *adapter) GetOnChainAIServiceURI(ctx context.Context, _ *protocolv1.Empty) (*protocolv1.OnChainAIServiceURIStatus, error) {
	out, err := a.srv.GetOnChainAIServiceURI(ctx, struct{}{})
	if err != nil {
		return nil, errorToStatus(err)
	}
	return pbOnChainAIServiceURIStatusFrom(out), nil
}

func (a *adapter) IsRegistered(ctx context.Context, _ *protocolv1.Empty) (*protocolv1.RegistrationStatus, error) {
	out, err := a.srv.IsRegistered(ctx, struct{}{})
	if err != nil {
		return nil, errorToStatus(err)
	}
	return pbRegistrationStatusFrom(out), nil
}

func (a *adapter) IsAIRegistered(ctx context.Context, _ *protocolv1.Empty) (*protocolv1.AIRegistrationStatus, error) {
	out, err := a.srv.IsAIRegistered(ctx, struct{}{})
	if err != nil {
		return nil, errorToStatus(err)
	}
	return pbAIRegistrationStatusFrom(out), nil
}

func (a *adapter) GetWalletBalance(ctx context.Context, _ *protocolv1.Empty) (*protocolv1.WalletBalanceStatus, error) {
	out, err := a.srv.GetWalletBalance(ctx, struct{}{})
	if err != nil {
		return nil, errorToStatus(err)
	}
	return pbWalletBalanceStatusFrom(out), nil
}

func (a *adapter) GetTxIntent(ctx context.Context, req *protocolv1.TxIntentRef) (*protocolv1.TxIntentSnapshot, error) {
	ref, ok := txIntentRefFromPB(req)
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "TxIntentRef.id must be 32 bytes")
	}
	snap, err := a.srv.GetTxIntent(ctx, ref)
	if err != nil {
		return nil, errorToStatus(err)
	}
	return pbTxIntentSnapshotFrom(snap), nil
}

func (a *adapter) StreamRoundEvents(_ *protocolv1.Empty, stream protocolv1.ProtocolDaemon_StreamRoundEventsServer) error {
	err := a.srv.StreamRoundEvents(stream.Context(), func(ev RoundEvent) error {
		return stream.Send(pbRoundEventFrom(ev))
	})
	// Server.StreamRoundEvents returns ctx.Err() (Canceled / DeadlineExceeded)
	// on shutdown — those are the normal way the stream ends and should not
	// be promoted to a status error here. nil falls through.
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return errorToStatus(err)
}

// errorToStatus maps an in-process sentinel to a gRPC status.
//
// nil → nil (caller short-circuits).
func errorToStatus(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrUnimplemented):
		return status.Error(codes.Unimplemented, err.Error())
	case errors.Is(err, ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
