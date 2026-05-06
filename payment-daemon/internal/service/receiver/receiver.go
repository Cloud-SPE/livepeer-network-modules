// Package receiver implements PayeeDaemon — validates incoming payment
// blobs, tracks per-(sender, work_id) balances, and (post chain
// integration) redeems winning tickets via the TicketBroker.
//
// v0.2 scope:
//   - OpenSession / ProcessPayment / DebitBalance / SufficientBalance /
//     GetBalance / CloseSession are real and persist to BoltDB.
//   - GetQuote / GetTicketParams return canned values: a stub
//     TicketParams the sender daemon also fabricates locally. Plan
//     0016 swaps the stubs for HTTP-fetched authoritative params.
//   - ListCapabilities returns whatever was loaded from the
//     capability-catalog YAML at startup.
//   - ListPendingRedemptions / GetRedemptionStatus return empty
//     responses; the redemption queue is plan 0016.
//   - Ticket validation is stubbed: any well-formed Payment bytes is
//     accepted; credited_ev is zero. Plan 0016 wires real ECDSA
//     recovery + win-prob evaluation + nonce ledger.
package receiver

import (
	"context"
	"encoding/hex"
	"errors"
	"log/slog"
	"math/big"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/store"
)

// Service implements pb.PayeeDaemonServer.
type Service struct {
	pb.UnimplementedPayeeDaemonServer

	store  *store.Store
	logger *slog.Logger
}

// New constructs a receiver Service backed by the given store.
func New(st *store.Store, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: st, logger: logger}
}

// ─── Sessions ─────────────────────────────────────────────────────────

// OpenSession idempotently creates a session for (work_id) with the
// given pricing metadata. The sender is sealed on the first
// ProcessPayment call.
func (s *Service) OpenSession(_ context.Context, req *pb.OpenSessionRequest) (*pb.OpenSessionResponse, error) {
	if req.GetWorkId() == "" {
		return nil, status.Error(codes.InvalidArgument, "work_id is empty")
	}
	if req.GetCapability() == "" {
		return nil, status.Error(codes.InvalidArgument, "capability is empty")
	}
	if req.GetOffering() == "" {
		return nil, status.Error(codes.InvalidArgument, "offering is empty")
	}
	if req.GetWorkUnit() == "" {
		return nil, status.Error(codes.InvalidArgument, "work_unit is empty")
	}
	priceWei := new(big.Int).SetBytes(req.GetPricePerWorkUnitWei())
	if priceWei.Sign() < 0 {
		return nil, status.Error(codes.InvalidArgument, "price_per_work_unit_wei must be >= 0")
	}

	_, alreadyOpen, err := s.store.OpenSession(store.Session{
		WorkID:              req.GetWorkId(),
		Capability:          req.GetCapability(),
		Offering:            req.GetOffering(),
		PricePerWorkUnitWei: priceWei.String(),
		WorkUnit:            req.GetWorkUnit(),
	})
	if err != nil {
		s.logger.Error("open session", "err", err)
		return nil, status.Errorf(codes.Internal, "open session: %v", err)
	}
	outcome := pb.OpenSessionResponse_OUTCOME_OPENED
	if alreadyOpen {
		outcome = pb.OpenSessionResponse_OUTCOME_ALREADY_OPEN
	}
	s.logger.Info("session opened",
		"work_id", req.GetWorkId(),
		"capability", req.GetCapability(),
		"offering", req.GetOffering(),
		"price_per_work_unit_wei", priceWei.String(),
		"already_open", alreadyOpen)
	return &pb.OpenSessionResponse{Outcome: outcome}, nil
}

// ProcessPayment decodes a wire Payment, seals the sender on the
// session, and credits zero EV (stub). Plan 0016 wires real validation.
func (s *Service) ProcessPayment(_ context.Context, req *pb.ProcessPaymentRequest) (*pb.ProcessPaymentResponse, error) {
	if req.GetWorkId() == "" {
		return nil, status.Error(codes.InvalidArgument, "work_id is empty")
	}
	if len(req.GetPaymentBytes()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "payment_bytes is empty")
	}
	var pay pb.Payment
	if err := proto.Unmarshal(req.GetPaymentBytes(), &pay); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode payment: %v", err)
	}
	if len(pay.GetSender()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "payment.sender is empty")
	}

	if err := s.store.SealSender(req.GetWorkId(), pay.GetSender()); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, status.Error(codes.FailedPrecondition, "no session for work_id; OpenSession first")
		case errors.Is(err, store.ErrSenderMismatch):
			return nil, status.Error(codes.FailedPrecondition, "payment sender does not match the session's sealed sender")
		default:
			return nil, status.Errorf(codes.Internal, "seal sender: %v", err)
		}
	}

	// v0.2: credit zero EV. Plan 0016 computes per-ticket EV from
	// face_value × win_prob and credits real wei.
	credited := big.NewInt(0)
	balance, err := s.store.CreditBalance(pay.GetSender(), req.GetWorkId(), credited)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "credit balance: %v", err)
	}

	s.logger.Info("payment processed",
		"work_id", req.GetWorkId(),
		"sender_hex", hex.EncodeToString(pay.GetSender()),
		"tickets", len(pay.GetTicketSenderParams()),
		"credited_ev_wei", credited.String(),
		"balance_wei", balance.String())

	return &pb.ProcessPaymentResponse{
		Sender:        pay.GetSender(),
		CreditedEv:    credited.Bytes(),
		Balance:       balance.Bytes(),
		WinnersQueued: 0, // win_prob is zero in v0.2 stub, so no winners
	}, nil
}

// DebitBalance subtracts (work_units × price) from the balance.
// Idempotent by (sender, work_id, debit_seq).
func (s *Service) DebitBalance(_ context.Context, req *pb.DebitBalanceRequest) (*pb.DebitBalanceResponse, error) {
	if len(req.GetSender()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "sender is empty")
	}
	if req.GetWorkId() == "" {
		return nil, status.Error(codes.InvalidArgument, "work_id is empty")
	}
	if req.GetWorkUnits() < 0 {
		return nil, status.Error(codes.InvalidArgument, "work_units must be >= 0")
	}
	balance, err := s.store.DebitBalance(req.GetSender(), req.GetWorkId(), req.GetWorkUnits(), req.GetDebitSeq())
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return &pb.DebitBalanceResponse{Balance: balance.Bytes()}, nil
}

// SufficientBalance reports whether the balance covers a minimum
// number of work units, without debiting.
func (s *Service) SufficientBalance(_ context.Context, req *pb.SufficientBalanceRequest) (*pb.SufficientBalanceResponse, error) {
	if len(req.GetSender()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "sender is empty")
	}
	if req.GetWorkId() == "" {
		return nil, status.Error(codes.InvalidArgument, "work_id is empty")
	}
	sess, err := s.store.Get(req.GetSender(), req.GetWorkId())
	if err != nil {
		return nil, mapStoreErr(err)
	}
	balance, _ := new(big.Int).SetString(sess.BalanceWei, 10)
	if balance == nil {
		balance = new(big.Int)
	}
	price, _ := new(big.Int).SetString(sess.PricePerWorkUnitWei, 10)
	if price == nil {
		price = new(big.Int)
	}
	required := new(big.Int).Mul(price, big.NewInt(req.GetMinWorkUnits()))
	return &pb.SufficientBalanceResponse{
		Sufficient: balance.Cmp(required) >= 0,
		Balance:    balance.Bytes(),
	}, nil
}

// GetBalance returns the current balance for (sender, work_id).
func (s *Service) GetBalance(_ context.Context, req *pb.GetBalanceRequest) (*pb.GetBalanceResponse, error) {
	if len(req.GetSender()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "sender is empty")
	}
	if req.GetWorkId() == "" {
		return nil, status.Error(codes.InvalidArgument, "work_id is empty")
	}
	balance, err := s.store.GetBalance(req.GetSender(), req.GetWorkId())
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return &pb.GetBalanceResponse{Balance: balance.Bytes()}, nil
}

// CloseSession finalizes the session.
func (s *Service) CloseSession(_ context.Context, req *pb.CloseSessionRequest) (*pb.CloseSessionResponse, error) {
	if len(req.GetSender()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "sender is empty")
	}
	if req.GetWorkId() == "" {
		return nil, status.Error(codes.InvalidArgument, "work_id is empty")
	}
	alreadyClosed, err := s.store.CloseSession(req.GetSender(), req.GetWorkId())
	if err != nil {
		return nil, mapStoreErr(err)
	}
	outcome := pb.CloseSessionResponse_OUTCOME_CLOSED
	if alreadyClosed {
		outcome = pb.CloseSessionResponse_OUTCOME_ALREADY_CLOSED
	}
	return &pb.CloseSessionResponse{Outcome: outcome}, nil
}

// ─── Stubs (plan 0016) ───────────────────────────────────────────────

// GetQuote returns a stub. Plan 0016 wires real receiver-issued
// TicketParams + per-offering pricing.
func (s *Service) GetQuote(_ context.Context, _ *pb.GetQuoteRequest) (*pb.GetQuoteResponse, error) {
	return nil, status.Error(codes.Unimplemented, "GetQuote is plan 0016")
}

// GetTicketParams returns a stub. Plan 0016 wires real authoritative
// TicketParams issuance.
func (s *Service) GetTicketParams(_ context.Context, _ *pb.GetTicketParamsRequest) (*pb.GetTicketParamsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "GetTicketParams is plan 0016")
}

// ListCapabilities returns an empty catalog. Plan 0016 wires worker.yaml
// loading.
func (s *Service) ListCapabilities(_ context.Context, _ *pb.ListCapabilitiesRequest) (*pb.ListCapabilitiesResponse, error) {
	return &pb.ListCapabilitiesResponse{}, nil
}

// ListPendingRedemptions returns an empty list. Plan 0016 wires the
// real redemption queue.
func (s *Service) ListPendingRedemptions(_ context.Context, _ *pb.ListPendingRedemptionsRequest) (*pb.ListPendingRedemptionsResponse, error) {
	return &pb.ListPendingRedemptionsResponse{}, nil
}

// GetRedemptionStatus returns STATUS_UNSPECIFIED for any ticket.
// Plan 0016 wires real chain queries.
func (s *Service) GetRedemptionStatus(_ context.Context, _ *pb.GetRedemptionStatusRequest) (*pb.GetRedemptionStatusResponse, error) {
	return &pb.GetRedemptionStatusResponse{Status: pb.GetRedemptionStatusResponse_STATUS_UNSPECIFIED}, nil
}

// Health returns "ok" — the broker probes this at startup.
func (s *Service) Health(_ context.Context, _ *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Status: "ok"}, nil
}

// ─── helpers ──────────────────────────────────────────────────────────

func mapStoreErr(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return status.Error(codes.NotFound, "session not found")
	case errors.Is(err, store.ErrClosed):
		return status.Error(codes.FailedPrecondition, "session is closed")
	case errors.Is(err, store.ErrSenderMismatch):
		return status.Error(codes.FailedPrecondition, "sender mismatch")
	default:
		return status.Errorf(codes.Internal, "%v", err)
	}
}
