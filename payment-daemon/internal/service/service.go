// Package service implements the PayeeDaemon gRPC service.
//
// Every RPC validates inputs, talks to the BoltDB store, and returns
// either a typed response or a gRPC status code. v0.1 ticket validation
// is a no-op: any non-empty ticket is accepted.
package service

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/proto/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/store"
)

// Service implements pb.PayeeDaemonServer.
type Service struct {
	pb.UnimplementedPayeeDaemonServer

	store  *store.Store
	logger *slog.Logger
}

// New returns a Service backed by the given store.
func New(st *store.Store, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: st, logger: logger}
}

// OpenSession validates the cross-check fields and creates a session row.
func (s *Service) OpenSession(ctx context.Context, req *pb.OpenSessionRequest) (*pb.OpenSessionResponse, error) {
	if req.GetPayment() == nil {
		return nil, status.Error(codes.InvalidArgument, "payment is required")
	}
	pay := req.GetPayment()
	if pay.GetCapabilityId() == "" {
		return nil, status.Error(codes.InvalidArgument, "payment.capability_id is empty")
	}
	if pay.GetOfferingId() == "" {
		return nil, status.Error(codes.InvalidArgument, "payment.offering_id is empty")
	}
	if pay.GetExpectedMaxUnits() == 0 {
		return nil, status.Error(codes.InvalidArgument, "payment.expected_max_units must be > 0")
	}
	if len(pay.GetTicket()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "payment.ticket is empty")
	}
	if req.GetCapabilityId() != "" && req.GetCapabilityId() != pay.GetCapabilityId() {
		return nil, status.Error(codes.InvalidArgument, "capability_id mismatch between header and payment envelope")
	}
	if req.GetOfferingId() != "" && req.GetOfferingId() != pay.GetOfferingId() {
		return nil, status.Error(codes.InvalidArgument, "offering_id mismatch between header and payment envelope")
	}

	sess, err := s.store.CreateSession(store.Session{
		CapabilityID:     pay.GetCapabilityId(),
		OfferingID:       pay.GetOfferingId(),
		Ticket:           pay.GetTicket(),
		ExpectedMaxUnits: pay.GetExpectedMaxUnits(),
	})
	if err != nil {
		s.logger.Error("create session", "err", err)
		return nil, status.Errorf(codes.Internal, "create session: %v", err)
	}
	s.logger.Info("session opened",
		"session_id", sess.ID,
		"capability_id", sess.CapabilityID,
		"offering_id", sess.OfferingID,
		"expected_max_units", sess.ExpectedMaxUnits)
	return &pb.OpenSessionResponse{SessionId: sess.ID}, nil
}

// Debit appends to the session's debit ledger.
func (s *Service) Debit(ctx context.Context, req *pb.DebitRequest) (*pb.DebitResponse, error) {
	if req.GetSessionId() == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is empty")
	}
	if req.GetUnits() == 0 {
		return nil, status.Error(codes.InvalidArgument, "units must be > 0")
	}
	if err := s.store.AppendDebit(req.GetSessionId(), req.GetUnits()); err != nil {
		return nil, mapStoreErr(err)
	}
	return &pb.DebitResponse{}, nil
}

// Reconcile records the post-handler actual unit count.
func (s *Service) Reconcile(ctx context.Context, req *pb.ReconcileRequest) (*pb.ReconcileResponse, error) {
	if req.GetSessionId() == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is empty")
	}
	if err := s.store.SetActualUnits(req.GetSessionId(), req.GetActualUnits()); err != nil {
		return nil, mapStoreErr(err)
	}
	return &pb.ReconcileResponse{}, nil
}

// CloseSession marks the session closed. Idempotent.
func (s *Service) CloseSession(ctx context.Context, req *pb.CloseSessionRequest) (*pb.CloseSessionResponse, error) {
	if req.GetSessionId() == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is empty")
	}
	if err := s.store.CloseSession(req.GetSessionId()); err != nil {
		return nil, mapStoreErr(err)
	}
	return &pb.CloseSessionResponse{}, nil
}

// Health returns "ok". The broker calls this once at startup.
func (s *Service) Health(ctx context.Context, _ *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Status: "ok"}, nil
}

func mapStoreErr(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return status.Error(codes.NotFound, "session not found")
	case errors.Is(err, store.ErrClosed):
		return status.Error(codes.FailedPrecondition, "session is closed")
	default:
		return status.Errorf(codes.Internal, "%v", err)
	}
}
