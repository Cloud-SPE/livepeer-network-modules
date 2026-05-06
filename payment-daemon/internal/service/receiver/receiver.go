// Package receiver implements PayeeDaemon — validates incoming payment
// blobs, tracks per-(sender, work_id) balances, and (post chain
// integration) redeems winning tickets via the TicketBroker.
package receiver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service/receiver/validator"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/types"
)

// Service implements pb.PayeeDaemonServer.
type Service struct {
	pb.UnimplementedPayeeDaemonServer

	store     *store.Store
	logger    *slog.Logger
	recipient []byte // 20-byte ETH address this daemon receives as

	// defaultFaceValue / defaultWinProb size newly-issued ticket
	// params. Operators tune these via the runbook (--receiver-ev,
	// --receiver-tx-cost-multiplier). Plan 0016 takes them at
	// constructor time; future plans can refine per-offering pricing.
	defaultFaceValue *big.Int
	defaultWinProb   *big.Int
}

// Config holds the receiver service's tunable state.
type Config struct {
	// Recipient is the 20-byte ETH address this daemon receives as.
	// Derived at boot from the keystore (or the --orch-address override
	// for hot/cold split).
	Recipient []byte

	// DefaultFaceValue is the face_value embedded in newly-issued
	// TicketParams. Nil = 1e15 wei (~0.001 ETH equivalent at typical
	// gas).
	DefaultFaceValue *big.Int

	// DefaultWinProb is the win-probability embedded in newly-issued
	// TicketParams. Nil = ~1/1024 (a sensible default from the runbook).
	DefaultWinProb *big.Int
}

// New constructs a receiver Service backed by the given store.
func New(st *store.Store, cfg Config, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	faceValue := cfg.DefaultFaceValue
	if faceValue == nil {
		faceValue = new(big.Int).Exp(big.NewInt(10), big.NewInt(15), nil)
	}
	winProb := cfg.DefaultWinProb
	if winProb == nil {
		// 1/1024 of MaxWinProb.
		winProb = new(big.Int).Quo(types.MaxWinProb, big.NewInt(1024))
	}
	return &Service{
		store:            st,
		logger:           logger,
		recipient:        append([]byte(nil), cfg.Recipient...),
		defaultFaceValue: faceValue,
		defaultWinProb:   winProb,
	}
}

// OpenSession idempotently creates a session. Issues a fresh
// recipient-rand secret on first open; the rand stays in the session
// record for the lifetime of the session and is revealed only on
// winning-ticket redemption.
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

	rand, err := genRand()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "gen rand: %v", err)
	}

	_, alreadyOpen, err := s.store.OpenSession(store.Session{
		WorkID:              req.GetWorkId(),
		Capability:          req.GetCapability(),
		Offering:            req.GetOffering(),
		PricePerWorkUnitWei: priceWei.String(),
		WorkUnit:            req.GetWorkUnit(),
		RecipientRand:       rand.String(),
		FaceValueWei:        s.defaultFaceValue.String(),
		WinProb:             s.defaultWinProb.String(),
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
// session, validates each ticket-sender-param against the session's
// recipient-rand secret, sums EV credit, and queues winners for
// redemption.
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

	sess, err := s.store.Get(pay.GetSender(), req.GetWorkId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load session: %v", err)
	}

	// Recover the per-session rand. Empty rand = session was opened by
	// the v0.2 stub flow before plan 0016 landed; we bypass chain
	// validation in that case to keep the dev path running. A real
	// chain-mode receiver always has a rand because OpenSession sets
	// one.
	var recipientRand *big.Int
	if sess.RecipientRand != "" {
		var ok bool
		recipientRand, ok = new(big.Int).SetString(sess.RecipientRand, 10)
		if !ok {
			return nil, status.Error(codes.Internal, "session rand corrupt")
		}
	}

	credited := big.NewInt(0)
	var winnersQueued int32
	if recipientRand != nil && pay.GetTicketParams() != nil {
		c, w, err := s.validateAndCredit(&pay, sess, recipientRand)
		if err != nil {
			return nil, err
		}
		credited = c
		winnersQueued = int32(w)
	}

	balance, err := s.store.CreditBalance(pay.GetSender(), req.GetWorkId(), credited)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "credit balance: %v", err)
	}

	s.logger.Info("payment processed",
		"work_id", req.GetWorkId(),
		"sender_hex", hex.EncodeToString(pay.GetSender()),
		"tickets", len(pay.GetTicketSenderParams()),
		"credited_ev_wei", credited.String(),
		"winners_queued", winnersQueued,
		"balance_wei", balance.String())

	return &pb.ProcessPaymentResponse{
		Sender:        pay.GetSender(),
		CreditedEv:    credited.Bytes(),
		Balance:       balance.Bytes(),
		WinnersQueued: winnersQueued,
	}, nil
}

// validateAndCredit walks every TicketSenderParam in a payment,
// reconstructs the underlying Ticket, validates it against the
// session's recipient-rand secret, records the nonce, sums EV credit,
// and queues winners for redemption. Per-ticket failures are logged but
// do not fail the entire payment — sender hostility / single-ticket
// corruption shouldn't poison legitimate tickets in the same batch.
func (s *Service) validateAndCredit(pay *pb.Payment, sess *store.Session, recipientRand *big.Int) (*big.Int, uint32, error) {
	creditTotal := new(big.Int)
	winners := uint32(0)

	tp := pay.GetTicketParams()
	exp := pay.GetExpirationParams()
	faceValue := new(big.Int).SetBytes(tp.GetFaceValue())
	winProb := new(big.Int).SetBytes(tp.GetWinProb())

	expRound := int64(0)
	var expHash []byte
	if exp != nil {
		expRound = exp.GetCreationRound()
		expHash = exp.GetCreationRoundBlockHash()
	}

	for _, tsp := range pay.GetTicketSenderParams() {
		ticket := &types.Ticket{
			Recipient:         tp.GetRecipient(),
			Sender:            pay.GetSender(),
			FaceValue:         faceValue,
			WinProb:           winProb,
			SenderNonce:       tsp.GetSenderNonce(),
			RecipientRandHash: tp.GetRecipientRandHash(),
			CreationRound:     expRound,
			CreationRoundHash: expHash,
		}
		if err := validator.Validate(s.recipient, ticket, tsp.GetSig(), recipientRand); err != nil {
			s.logger.Warn("invalid ticket; skipping",
				"work_id", sess.WorkID,
				"nonce", tsp.GetSenderNonce(),
				"err", err)
			continue
		}
		if err := s.store.RecordNonce(recipientRand, tsp.GetSenderNonce()); err != nil {
			if errors.Is(err, store.ErrNonceAlreadySeen) {
				s.logger.Warn("nonce replay; skipping",
					"work_id", sess.WorkID,
					"nonce", tsp.GetSenderNonce())
				continue
			}
			if errors.Is(err, store.ErrTooManyNonces) {
				s.logger.Warn("nonce cap reached; skipping ticket and remaining batch",
					"work_id", sess.WorkID,
					"nonce", tsp.GetSenderNonce())
				return creditTotal, winners, nil
			}
			return nil, 0, status.Errorf(codes.Internal, "record nonce: %v", err)
		}
		// EV credit: face_value × win_prob / 2^256, integer floor.
		ev := types.EV(faceValue, winProb)
		if ev != nil {
			num := new(big.Int).Quo(ev.Num(), ev.Denom())
			creditTotal.Add(creditTotal, num)
		}
		if validator.IsWinning(ticket, tsp.GetSig(), recipientRand) {
			st := &store.SignedTicket{
				Recipient:         ticket.Recipient,
				Sender:            ticket.Sender,
				FaceValue:         new(big.Int).Set(faceValue),
				WinProb:           new(big.Int).Set(winProb),
				SenderNonce:       tsp.GetSenderNonce(),
				RecipientRandHash: ticket.RecipientRandHash,
				CreationRound:     ticket.CreationRound,
				CreationRoundHash: append([]byte(nil), ticket.CreationRoundHash...),
				Sig:               append([]byte(nil), tsp.GetSig()...),
				RecipientRand:     new(big.Int).Set(recipientRand),
			}
			enqueued, err := s.store.EnqueueRedemption(ticket.Hash(), st)
			if err != nil {
				return nil, 0, status.Errorf(codes.Internal, "enqueue redemption: %v", err)
			}
			if enqueued {
				winners++
				s.logger.Info("winner queued",
					"work_id", sess.WorkID,
					"ticket_hash", hex.EncodeToString(ticket.Hash()),
					"face_value_wei", faceValue.String())
			}
		}
	}
	return creditTotal, winners, nil
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

// GetTicketParams issues a fresh recipient-rand secret, derives the
// work_id (hex of the rand-hash), opens an idempotent session bound to
// (sender, capability, offering), and returns the authoritative
// TicketParams. The rand preimage stays in the receiver's store and is
// revealed only when redeeming a winning ticket on-chain.
//
// Idempotency: the same (sender, capability, offering) triple
// re-issuing within the lifetime of an open session reuses the
// existing rand. Re-issuing after the session has been closed
// generates a fresh rand (and thus a fresh work_id).
func (s *Service) GetTicketParams(_ context.Context, req *pb.GetTicketParamsRequest) (*pb.GetTicketParamsResponse, error) {
	if len(req.GetSender()) != 20 {
		return nil, status.Error(codes.InvalidArgument, "sender must be 20 bytes")
	}
	if req.GetCapability() == "" {
		return nil, status.Error(codes.InvalidArgument, "capability is empty")
	}
	if req.GetOffering() == "" {
		return nil, status.Error(codes.InvalidArgument, "offering is empty")
	}
	if got := req.GetRecipient(); len(got) != 0 && !equalBytes(got, s.recipient) {
		return nil, status.Error(codes.InvalidArgument, "recipient mismatch")
	}

	r, err := genRand()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "gen rand: %v", err)
	}
	rrHash := types.HashRecipientRand(r)
	workID := hex.EncodeToString(rrHash)

	faceValue := new(big.Int).Set(s.defaultFaceValue)
	if got := req.GetFaceValue(); len(got) > 0 {
		// Honor the sender's face-value request when economically
		// reasonable. Plan-future: real per-offering pricing.
		faceValue = new(big.Int).SetBytes(got)
	}

	_, _, err = s.store.OpenSession(store.Session{
		WorkID:              workID,
		Capability:          req.GetCapability(),
		Offering:            req.GetOffering(),
		PricePerWorkUnitWei: "0",
		WorkUnit:            "ticket",
		RecipientRand:       r.String(),
		FaceValueWei:        faceValue.String(),
		WinProb:             s.defaultWinProb.String(),
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "open session: %v", err)
	}

	return &pb.GetTicketParamsResponse{
		TicketParams: &pb.TicketParams{
			Recipient:         append([]byte(nil), s.recipient...),
			FaceValue:         faceValue.Bytes(),
			WinProb:           s.defaultWinProb.Bytes(),
			RecipientRandHash: rrHash,
			Seed:              []byte{},
		},
	}, nil
}

// GetQuote returns a stub. Per-offering pricing is a future plan.
func (s *Service) GetQuote(_ context.Context, _ *pb.GetQuoteRequest) (*pb.GetQuoteResponse, error) {
	return nil, status.Error(codes.Unimplemented, "GetQuote not implemented; per-offering pricing is a future plan")
}

// ListCapabilities returns an empty catalog. Capability-catalog wiring
// is a future plan.
func (s *Service) ListCapabilities(_ context.Context, _ *pb.ListCapabilitiesRequest) (*pb.ListCapabilitiesResponse, error) {
	return &pb.ListCapabilitiesResponse{}, nil
}

// ListPendingRedemptions reads the queued winners from the redemptions
// store.
func (s *Service) ListPendingRedemptions(_ context.Context, _ *pb.ListPendingRedemptionsRequest) (*pb.ListPendingRedemptionsResponse, error) {
	pend, err := s.store.PendingRedemptions()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list pending: %v", err)
	}
	out := make([]*pb.PendingRedemption, 0, len(pend))
	for _, p := range pend {
		out = append(out, &pb.PendingRedemption{
			Sender:     p.Ticket.Sender,
			TicketHash: p.Hash,
			FaceValue:  p.Ticket.FaceValue.Bytes(),
		})
	}
	return &pb.ListPendingRedemptionsResponse{Redemptions: out}, nil
}

// GetRedemptionStatus reports whether a specific ticket-hash has been
// queued, redeemed, or never seen.
func (s *Service) GetRedemptionStatus(_ context.Context, req *pb.GetRedemptionStatusRequest) (*pb.GetRedemptionStatusResponse, error) {
	if len(req.GetTicketHash()) != 32 {
		return nil, status.Error(codes.InvalidArgument, "ticket_hash must be 32 bytes")
	}
	txHash, err := s.store.RedeemedTxHash(req.GetTicketHash())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "lookup: %v", err)
	}
	if txHash != nil {
		// All-zero tx hash = drained locally without on-chain
		// redemption (terminal pre-check failure).
		zero := make([]byte, 32)
		if equalBytes(txHash, zero) {
			return &pb.GetRedemptionStatusResponse{Status: pb.GetRedemptionStatusResponse_STATUS_FAILED}, nil
		}
		return &pb.GetRedemptionStatusResponse{
			Status: pb.GetRedemptionStatusResponse_STATUS_CONFIRMED,
			TxHash: txHash,
		}, nil
	}
	pend, err := s.store.PendingRedemptions()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "lookup pending: %v", err)
	}
	for _, p := range pend {
		if equalBytes(p.Hash, req.GetTicketHash()) {
			return &pb.GetRedemptionStatusResponse{Status: pb.GetRedemptionStatusResponse_STATUS_QUEUED}, nil
		}
	}
	return &pb.GetRedemptionStatusResponse{Status: pb.GetRedemptionStatusResponse_STATUS_UNSPECIFIED}, nil
}

// Health returns "ok" — the broker probes this at startup.
func (s *Service) Health(_ context.Context, _ *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Status: "ok"}, nil
}

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

// genRand returns a 256-bit random non-negative integer used as the
// recipient-rand secret. Stored only on the receiver; revealed only on
// redemption.
func genRand() (*big.Int, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return nil, fmt.Errorf("crypto/rand: %w", err)
	}
	return new(big.Int).SetBytes(buf[:]), nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
