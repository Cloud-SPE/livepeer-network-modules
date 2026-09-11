package sender

import (
	"context"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RoundAdvancer is a clock whose rounds can be moved on demand. Only the
// dev clock implements it; the on-chain clock deliberately does not,
// because its rounds are the chain's.
type RoundAdvancer interface {
	AdvanceRounds(n int64) int64
	LastInitializedRound() int64
}

// AdminService implements PayerAdmin.
type AdminService struct {
	pb.UnimplementedPayerAdminServer
	clock RoundAdvancer
}

// NewAdmin returns the admin service. A nil clock means the daemon is
// not on a dev clock, and every RPC refuses.
func NewAdmin(clock RoundAdvancer) *AdminService {
	return &AdminService{clock: clock}
}

// AdvanceDevRound moves the dev clock forward.
//
// Refused on a real chain clock. A daemon that could fake rounds could
// make an expired envelope look live to anything reading its clock,
// which is the one thing the release rule depends on being honest.
func (s *AdminService) AdvanceDevRound(_ context.Context, req *pb.AdvanceDevRoundRequest) (*pb.AdvanceDevRoundResponse, error) {
	if s.clock == nil {
		return nil, status.Error(codes.FailedPrecondition,
			"round advancement is dev-clock only; this daemon is on a chain clock and its "+
				"rounds are the chain's")
	}
	if req.GetRounds() < 1 {
		return nil, status.Error(codes.InvalidArgument, "rounds must be >= 1")
	}
	return &pb.AdvanceDevRoundResponse{CurrentRound: s.clock.AdvanceRounds(req.GetRounds())}, nil
}
