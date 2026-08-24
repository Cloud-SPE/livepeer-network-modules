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

	ethcommon "github.com/ethereum/go-ethereum/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/service/receiver/validator"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/store"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/types"
)

// Service implements pb.PayeeDaemonServer.
type Service struct {
	pb.UnimplementedPayeeDaemonServer
	pb.UnimplementedPayeeAdminServer

	store     *store.Store
	logger    *slog.Logger
	metrics   metrics.Recorder
	recipient []byte // 20-byte ETH address this daemon receives as

	// defaultFaceValue / defaultWinProb size newly-issued ticket
	// params. Operators tune these via the runbook (--receiver-ev,
	// --receiver-tx-cost-multiplier). Plan 0016 takes them at
	// constructor time; future plans can refine per-offering pricing.
	defaultFaceValue *big.Int
	defaultWinProb   *big.Int
	// minFaceValue is the smallest ticket this payee will issue. Below
	// it, EV credit floors to zero and a winning ticket costs more gas
	// to redeem than it pays.
	minFaceValue *big.Int
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

	// Recorder receives domain metrics. Nil = a no-op recorder.
	Recorder metrics.Recorder
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
	// The floor must make EV land above zero: EV is
	// face_value x win_prob / 2^256, so the smallest useful face value
	// is 2^256 / win_prob — one wei of credit per ticket. Operators who
	// want more headroom raise DefaultFaceValue; this is the hard
	// minimum below which tickets are worthless by arithmetic.
	winProb := cfg.DefaultWinProb
	if winProb == nil {
		// 1/1024 of MaxWinProb.
		winProb = new(big.Int).Quo(types.MaxWinProb, big.NewInt(1024))
	}
	// One wei of credit per ticket is the arithmetic floor. Credit is
	// floor(face_value x win_prob / MaxWinProb), so crediting at least
	// one wei needs face_value >= MaxWinProb/win_prob — and that has to
	// round UP.
	//
	// It used to floor. At the defaults (win_prob = MaxWinProb/1024)
	// that advertised a minimum of 1024, and MaxWinProb is odd so
	// 1024 x win_prob < MaxWinProb: the payee accepted its own
	// advertised minimum and credited zero. Work served, nothing paid —
	// the same shape as the zero-credit payment found on mainnet, this
	// time reachable through the documented boundary rather than a bug.
	// The correct minimum for those defaults is 1025.
	minFace, rem := new(big.Int).QuoRem(types.MaxWinProb, winProb, new(big.Int))
	if rem.Sign() != 0 {
		minFace.Add(minFace, big.NewInt(1))
	}
	if minFace.Sign() <= 0 {
		minFace = big.NewInt(1)
	}
	// The floor used to be clamped DOWN to the operator's default face
	// value, so "an operator choosing a small default is not overridden."
	// That defeated the floor exactly when it was needed: a default
	// below the arithmetic minimum makes every ticket this payee issues
	// credit zero. The floor is not a preference, so the default is
	// raised to meet it and the operator is told.
	if faceValue.Cmp(minFace) < 0 {
		logger.Warn("default face value is below the arithmetic minimum; raising it",
			"configured_wei", faceValue.String(),
			"minimum_wei", minFace.String(),
			"reason", "below this, EV credit floors to zero and tickets are free money for the sender")
		faceValue = new(big.Int).Set(minFace)
	}
	rec := cfg.Recorder
	if rec == nil {
		rec = metrics.NewNoop()
	}
	return &Service{
		store:            st,
		logger:           logger,
		metrics:          rec,
		recipient:        append([]byte(nil), cfg.Recipient...),
		defaultFaceValue: faceValue,
		defaultWinProb:   winProb,
		minFaceValue:     minFace,
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
		PerUnits:            req.GetPerUnits(),
		WorkUnit:            req.GetWorkUnit(),
		RecipientRand:       rand.String(),
		FaceValueWei:        s.defaultFaceValue.String(),
		WinProb:             s.defaultWinProb.String(),
	})
	if err != nil {
		if errors.Is(err, store.ErrPricingConflict) {
			s.logger.Error("open session: pricing conflict",
				"work_id", req.GetWorkId(), "offered_price_wei", priceWei.String())
			return nil, status.Error(codes.FailedPrecondition,
				"session price was already set by an offering and cannot be changed")
		}
		s.logger.Error("open session", "err", err)
		return nil, status.Errorf(codes.Internal, "open session: %v", err)
	}
	outcome := pb.OpenSessionResponse_OUTCOME_OPENED
	if alreadyOpen {
		outcome = pb.OpenSessionResponse_OUTCOME_ALREADY_OPEN
		s.metrics.IncSessionEvent(metrics.SessionAlreadyOpen)
	} else {
		s.metrics.IncSessionEvent(metrics.SessionOpened)
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

	// A closed session takes no more money.
	//
	// Closing is how a payee retires an identity — ResetSession marks the
	// old session closed and drops its index entry so the next
	// ticket-params issuance mints a fresh work_id. A payer that has not
	// yet learned of the rotation keeps paying the old one, and until
	// this guard existed those payments were accepted: validateAndCredit
	// ran, winning tickets were QUEUED FOR REDEMPTION, and the EV landed
	// on a session whose every debit fails with ErrClosed. The payer paid
	// real ETH into a session that can never serve it work.
	//
	// The refusal is returned in band rather than as an error because
	// that is how the rotation signal travels: the broker rebinds on
	// tickets_rejected > 0 with a dominant INVALID_RECIPIENT_RAND, and a
	// gRPC error would read as a generic failure and strand the payer on
	// the dead identity. The reason is the honest one — the recipient
	// rand behind this work_id is no longer one the payee will honour.
	if sess.Closed {
		statuses := make([]*pb.TicketStatus, 0, len(pay.GetTicketSenderParams()))
		for _, tsp := range pay.GetTicketSenderParams() {
			statuses = append(statuses, &pb.TicketStatus{
				SenderNonce:     tsp.GetSenderNonce(),
				RejectionReason: pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_INVALID_RECIPIENT_RAND,
			})
		}
		if len(statuses) == 0 {
			// A ticketless payment still has to carry the signal:
			// tickets_rejected == 0 reads as "accepted" downstream.
			statuses = append(statuses, &pb.TicketStatus{
				RejectionReason: pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_INVALID_RECIPIENT_RAND,
			})
		}
		rejected, dominant := summarizeTicketStatus(statuses)
		balance, ok := new(big.Int).SetString(sess.BalanceWei, 10)
		if !ok {
			balance = big.NewInt(0)
		}
		s.logger.Info("payment refused: session closed",
			"work_id", req.GetWorkId(),
			"sender_hex", hex.EncodeToString(pay.GetSender()),
			"tickets_refused", rejected)
		return &pb.ProcessPaymentResponse{
			Sender:            pay.GetSender(),
			CreditedEv:        big.NewInt(0).Bytes(),
			Balance:           balance.Bytes(),
			TicketStatus:      statuses,
			TicketsRejected:   rejected,
			DominantRejection: dominant,
		}, nil
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

	// Cross-check the price against what the SENDER signed.
	//
	// Until here the price is an assertion by the broker — the party
	// being paid. expected_price rides inside the payment the sender
	// signed, so it is the only figure both sides committed to. A
	// mismatch means the two disagree about the rate, and billing at
	// either number would charge somebody something they never agreed
	// to, so the payment is refused instead.
	if err := checkSignedPrice(&pay, sess); err != nil {
		return nil, err
	}

	credited := big.NewInt(0)
	var winnersQueued int32
	var ticketStatus []*pb.TicketStatus
	var ticketsRejected int32
	dominantRejection := pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_UNSPECIFIED
	if recipientRand != nil && pay.GetTicketParams() != nil {
		c, w, statuses, err := s.validateAndCredit(&pay, sess, recipientRand)
		if err != nil {
			return nil, err
		}
		credited = c
		winnersQueued = int32(w)
		ticketStatus = statuses
		ticketsRejected, dominantRejection = summarizeTicketStatus(statuses)
	}

	balance, err := s.store.CreditBalance(pay.GetSender(), req.GetWorkId(), credited)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "credit balance: %v", err)
	}

	s.recordPaymentMetrics(ticketStatus, winnersQueued, credited)

	s.logger.Info("payment processed",
		"work_id", req.GetWorkId(),
		"sender_hex", hex.EncodeToString(pay.GetSender()),
		"tickets", len(pay.GetTicketSenderParams()),
		"credited_ev_wei", credited.String(),
		"winners_queued", winnersQueued,
		"balance_wei", balance.String())

	return &pb.ProcessPaymentResponse{
		Sender:            pay.GetSender(),
		CreditedEv:        credited.Bytes(),
		Balance:           balance.Bytes(),
		WinnersQueued:     winnersQueued,
		TicketStatus:      ticketStatus,
		TicketsRejected:   ticketsRejected,
		DominantRejection: dominantRejection,
	}, nil
}

// validateAndCredit walks every TicketSenderParam in a payment,
// reconstructs the underlying Ticket, validates it against the
// session's recipient-rand secret, records the nonce, sums EV credit,
// and queues winners for redemption. Per-ticket failures are logged but
// do not fail the entire payment — sender hostility / single-ticket
// corruption shouldn't poison legitimate tickets in the same batch.
func (s *Service) validateAndCredit(pay *pb.Payment, sess *store.Session, recipientRand *big.Int) (*big.Int, uint32, []*pb.TicketStatus, error) {
	creditTotal := new(big.Int)
	winners := uint32(0)
	statuses := make([]*pb.TicketStatus, 0, len(pay.GetTicketSenderParams()))

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
		ticketStatus := &pb.TicketStatus{
			SenderNonce: tsp.GetSenderNonce(),
		}
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
			ticketStatus.RejectionReason = validationErrorReason(err)
			statuses = append(statuses, ticketStatus)
			s.logger.Warn("invalid ticket; skipping",
				"work_id", sess.WorkID,
				"nonce", tsp.GetSenderNonce(),
				"err", err)
			continue
		}
		if err := s.store.RecordNonce(recipientRand, tsp.GetSenderNonce()); err != nil {
			if errors.Is(err, store.ErrNonceAlreadySeen) {
				ticketStatus.RejectionReason = pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_NONCE_REPLAY
				statuses = append(statuses, ticketStatus)
				s.logger.Warn("nonce replay; skipping",
					"work_id", sess.WorkID,
					"nonce", tsp.GetSenderNonce())
				// NOT treated as a rotation trigger.
				//
				// A replay is ordinarily a duplicate delivery — the same
				// payment arriving twice, already credited, nothing to
				// do. It is ALSO what a payer that lost its durable
				// watermark looks like: its stream restarts low, every
				// nonce it produces has been seen, and it can never make
				// progress on this rand again.
				//
				// Those two cannot be told apart from one payment. A
				// re-delivered early payment replays a low nonce exactly
				// as a rewound sender does, so a positional rule
				// ("replayed far below the high-water mark") rotates the
				// route's identity on every ordinary retry. Recovering
				// the rewound case needs the payee to report its
				// high-water nonce so the payer can resync deliberately,
				// rather than this side guessing. Tracked separately;
				// see lnm-nbx.
				continue
			}
			if errors.Is(err, store.ErrTooManyNonces) {
				// The rand's nonce budget is spent. Retire it here and
				// report the result as a rotation, so recovery runs
				// through the path both sides already implement.
				//
				// The payee is the only party that authoritatively knows
				// this count — the payer's view is an estimate that a
				// restart or a partial state loss can put out of step —
				// so this is where the decision has to be made and acted
				// on in one step.
				//
				// Compare-and-swap against THIS session's work_id:
				// several payments can arrive at an exhausted rand at
				// once, and without the comparison the second would
				// retire the successor the first just created.
				rotated, rerr := s.store.ResetTicketSessionIfCurrent(store.TicketSessionKey{
					Sender:     sess.Sender,
					Recipient:  s.recipient,
					Capability: sess.Capability,
					Offering:   sess.Offering,
				}, sess.WorkID)
				if rerr != nil {
					return nil, 0, nil, status.Errorf(codes.Internal,
						"retiring exhausted ticket session: %v", rerr)
				}
				// Normalized to the existing contract rather than
				// surfaced as its own terminal reason. A caller seeing
				// NONCE_CAP_REACHED has nothing to do with it; one
				// seeing INVALID_RECIPIENT_RAND evicts its cache, mints
				// a successor and rebinds — machinery that already
				// exists on every side of this. The cause is in the log,
				// where an operator needs it; the wire carries the
				// recovery the caller can act on.
				ticketStatus.RejectionReason = pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_INVALID_RECIPIENT_RAND
				statuses = append(statuses, ticketStatus)
				s.logger.Warn("nonce budget exhausted; retiring the rand and reporting rotation",
					"work_id", sess.WorkID,
					"nonce", tsp.GetSenderNonce(),
					"cap", store.MaxSenderNonces,
					"rotated_by_this_call", rotated)
				return creditTotal, winners, statuses, nil
			}
			return nil, 0, nil, status.Errorf(codes.Internal, "record nonce: %v", err)
		}
		// EV credit: floor(face_value x win_prob / MaxWinProb).
		// Shared with the sender, which sizes its batch from the same
		// function — so "the payee credits at least what was funded" is
		// true by construction rather than by two implementations
		// happening to round the same way.
		num := types.CreditedEV(faceValue, winProb)
		creditTotal.Add(creditTotal, num)
		ticketStatus.CreditedEv = num.Bytes()
		if validator.IsWinning(ticket, tsp.GetSig(), recipientRand) {
			ticketStatus.WasWinning = true
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
				return nil, 0, nil, status.Errorf(codes.Internal, "enqueue redemption: %v", err)
			}
			if enqueued {
				winners++
				s.logger.Info("winner queued",
					"work_id", sess.WorkID,
					"ticket_hash", hex.EncodeToString(ticket.Hash()),
					"face_value_wei", faceValue.String())
			}
		}
		statuses = append(statuses, ticketStatus)
	}
	return creditTotal, winners, statuses, nil
}

// recordPaymentMetrics emits per-ticket accept/reject, winning-ticket,
// and credited-EV metrics for one processed payment.
func (s *Service) recordPaymentMetrics(statuses []*pb.TicketStatus, winnersQueued int32, credited *big.Int) {
	for _, st := range statuses {
		if st.GetRejectionReason() == pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_UNSPECIFIED {
			s.metrics.IncTicket(metrics.TicketAccepted)
			continue
		}
		s.metrics.IncTicket(metrics.TicketRejected)
		s.metrics.IncTicketRejected(rejectionReasonLabel(st.GetRejectionReason()))
	}
	for i := int32(0); i < winnersQueued; i++ {
		s.metrics.IncWinningTicket()
	}
	if credited != nil && credited.Sign() > 0 {
		s.metrics.AddCreditedEVGwei(metrics.WeiToGwei(credited))
	}
}

// rejectionReasonLabel maps a proto rejection reason to a bounded metric
// label.
func rejectionReasonLabel(r pb.PaymentRejectionReason) string {
	switch r {
	case pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_INVALID_RECIPIENT_RAND:
		return metrics.ReasonInvalidRecipientRand
	case pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_NONCE_REPLAY:
		return metrics.ReasonNonceReplay
	case pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_NONCE_CAP_REACHED:
		return metrics.ReasonNonceCap
	case pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_INVALID_SIGNATURE:
		return metrics.ReasonInvalidSignature
	default:
		return metrics.ReasonOther
	}
}

func validationErrorReason(err error) pb.PaymentRejectionReason {
	switch {
	case errors.Is(err, validator.ErrInvalidRecipientRand):
		return pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_INVALID_RECIPIENT_RAND
	case errors.Is(err, validator.ErrInvalidSignature):
		return pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_INVALID_SIGNATURE
	default:
		return pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_OTHER
	}
}

func summarizeTicketStatus(statuses []*pb.TicketStatus) (int32, pb.PaymentRejectionReason) {
	if len(statuses) == 0 {
		return 0, pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_UNSPECIFIED
	}
	counts := map[pb.PaymentRejectionReason]int32{}
	var rejected int32
	dominant := pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_UNSPECIFIED
	var dominantCount int32
	for _, st := range statuses {
		if st.GetRejectionReason() == pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_UNSPECIFIED {
			continue
		}
		rejected++
		counts[st.GetRejectionReason()]++
		if counts[st.GetRejectionReason()] > dominantCount {
			dominant = st.GetRejectionReason()
			dominantCount = counts[st.GetRejectionReason()]
		}
	}
	return rejected, dominant
}

// checkSignedPrice compares the session's price against the payment's
// signed expected_price. A payment carrying no expected_price is a
// stub/legacy blob and is tolerated — those cannot be minted against a
// real deposit, so there is nothing to protect.
//
// A session whose price is still unset is also tolerated: the broker's
// OpenSession has not run yet, and DebitBalance refuses to bill an
// unpriced session anyway.
func checkSignedPrice(pay *pb.Payment, sess *store.Session) error {
	price := pay.GetExpectedPrice()
	if price == nil || sess == nil || sess.PricePerWorkUnitWei == store.PricingUnset {
		return nil
	}
	signed := big.NewInt(price.GetPricePerUnit())
	stored, ok := new(big.Int).SetString(sess.PricePerWorkUnitWei, 10)
	if !ok || stored == nil {
		return status.Error(codes.Internal, "session price corrupt")
	}
	if signed.Cmp(stored) != 0 {
		return status.Errorf(codes.FailedPrecondition,
			"payment signed price %s wei does not match the session price %s wei",
			signed, stored)
	}
	signedPerUnits := price.GetPixelsPerUnit()
	if signedPerUnits <= 0 {
		signedPerUnits = 1
	}
	storedPerUnits := sess.PerUnits
	if storedPerUnits == 0 {
		storedPerUnits = 1
	}
	if uint64(signedPerUnits) != storedPerUnits {
		return status.Errorf(codes.FailedPrecondition,
			"payment signed per_units %d does not match the session per_units %d",
			signedPerUnits, storedPerUnits)
	}
	return nil
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
	res, err := s.store.DebitBalance(req.GetSender(), req.GetWorkId(), req.GetWorkUnits(), req.GetDebitSeq())
	if err != nil {
		s.metrics.IncDebit(metrics.ResultError)
		return nil, mapStoreErr(err)
	}
	s.metrics.IncDebit(metrics.ResultOK)
	s.metrics.AddWorkUnitsDebited(float64(req.GetWorkUnits()))
	// Report what was actually charged. The caller must not recompute
	// it: billing is cumulative, so the amount depends on the running
	// total and a recomputation from units alone disagrees whenever a
	// remainder carries.
	return &pb.DebitBalanceResponse{
		Balance:         res.Balance.Bytes(),
		DebitedWei:      &pb.BigUInt{Value: res.DebitedWei.Bytes()},
		CumulativeUnits: res.CumulativeUnits,
		Replayed:        res.Replayed,
	}, nil
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
	// Price the runway the way it will actually be debited: the
	// difference between cumulative bills, not the units in isolation.
	// Asking with the isolated price would over-state what the next
	// tick costs whenever the denominator is > 1.
	min := req.GetMinWorkUnits()
	if min < 0 {
		min = 0
	}
	required := new(big.Int).Sub(
		store.BillFor(price, sess.PerUnits, sess.DebitedUnits+uint64(min)),
		store.BillFor(price, sess.PerUnits, sess.DebitedUnits))
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
	} else {
		s.metrics.IncSessionEvent(metrics.SessionClosed)
	}
	return &pb.CloseSessionResponse{Outcome: outcome}, nil
}

// ResetSession forcibly rotates the active sender/payee session keyed
// by the stable sender/recipient/capability/offering identity.
func (s *Service) ResetSession(_ context.Context, req *pb.ResetSessionRequest) (*pb.ResetSessionResponse, error) {
	if len(req.GetSender()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "sender is empty")
	}
	if got := req.GetRecipient(); len(got) != 0 && !equalBytes(got, s.recipient) {
		return nil, status.Error(codes.InvalidArgument, "recipient mismatch")
	}
	if req.GetCapability() == "" {
		return nil, status.Error(codes.InvalidArgument, "capability is empty")
	}
	if req.GetOffering() == "" {
		return nil, status.Error(codes.InvalidArgument, "offering is empty")
	}
	oldWorkID, reset, err := s.store.ResetTicketSession(store.TicketSessionKey{
		Sender:     req.GetSender(),
		Recipient:  s.recipient,
		Capability: req.GetCapability(),
		Offering:   req.GetOffering(),
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "reset session: %v", err)
	}
	if reset {
		s.logger.Warn("session reset",
			"old_work_id", oldWorkID,
			"sender_hex", hex.EncodeToString(req.GetSender()),
			"capability", req.GetCapability(),
			"offering", req.GetOffering())
	}
	return &pb.ResetSessionResponse{
		Reset_:    reset,
		OldWorkId: oldWorkID,
	}, nil
}

// GetTicketParams reuses or mints the receiver-side recipient-rand
// secret for the stable (sender, recipient, capability, offering)
// identity, derives the work_id (hex of the rand-hash), and returns the
// authoritative TicketParams. The rand preimage stays in the receiver's
// store and is revealed only when redeeming a winning ticket on-chain.
//
// Idempotency: the same stable identity re-issuing within the lifetime
// of an open session reuses the existing rand, including across daemon
// restarts because the active session mapping lives in BoltDB.
// Re-issuing after the session has been closed generates a fresh rand
// (and thus a fresh work_id).
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
	var requestedFace *big.Int
	if got := req.GetFaceValue(); len(got) > 0 {
		requested := new(big.Int).SetBytes(got)
		// A sender may size its own tickets, but not below the floor.
		//
		// Credit is floor(face_value x win_prob / 2^256), so a small
		// enough face value makes every ticket credit ZERO while still
		// looking valid — the sender gets work for money that rounds
		// away. The floor is also plain economics: redeeming a winner
		// costs gas, and a face value under that is not worth winning.
		if requested.Cmp(s.minFaceValue) < 0 {
			return nil, status.Errorf(codes.InvalidArgument,
				"requested face_value %s wei is below this payee's minimum of %s wei",
				requested, s.minFaceValue)
		}
		faceValue = requested
		requestedFace = requested
	}

	tupleKey := store.TicketSessionKey{
		Sender:     req.GetSender(),
		Recipient:  s.recipient,
		Capability: req.GetCapability(),
		Offering:   req.GetOffering(),
	}

	// Rotate before handing out params the sender cannot use.
	//
	// A recipient rand tracks at most store.MaxSenderNonces nonces.
	// Beyond that every ticket on it is refused NONCE_CAP_REACHED and
	// credits nothing — so a sender that kept minting against an
	// exhausted rand would sign payments this payee has already decided
	// to reject. The store comment on MaxSenderNonces names this exit:
	// "beyond this the receiver should re-quote with a fresh
	// recipientRandHash". This is where that happens.
	//
	// Rotation retires the exhausted identity: ResetTicketSession closes
	// the old session, so a late payment on it is refused
	// recipient_rotated rather than credited to a session nobody can
	// draw on.
	//
	// Idempotent by construction. The check is on the CONSUMED budget of
	// the rand currently indexed for the tuple, so once rotated the new
	// rand has zero nonces and no further call rotates again. Two
	// concurrent callers both see the exhausted rand, but
	// ResetTicketSession is a single Bolt transaction, so exactly one
	// performs the reset and the other observes the successor.
	var predecessorWorkID string
	if existing, lookupErr := s.store.TicketSessionFor(tupleKey); lookupErr == nil && existing != nil {
		if rand, ok := new(big.Int).SetString(existing.RecipientRand, 10); ok {
			used, cerr := s.store.NonceCount(rand)
			if cerr != nil {
				return nil, status.Errorf(codes.Internal, "nonce budget: %v", cerr)
			}
			if used >= store.MaxSenderNonces {
				old, reset, rerr := s.store.ResetTicketSession(tupleKey)
				if rerr != nil {
					return nil, status.Errorf(codes.Internal, "rotate exhausted session: %v", rerr)
				}
				if reset {
					predecessorWorkID = old
					s.logger.Info("ticket session rotated: nonce budget exhausted",
						"predecessor_work_id", old,
						"nonces_used", used,
						"cap", store.MaxSenderNonces)
				}
			}
		}
	}

	sess, _, err := s.store.GetOrCreateTicketSession(tupleKey, store.Session{
		WorkID: workID,
		// This call mints ticket params; it has no idea what the work
		// costs. The broker's OpenSession sets the real price exactly
		// once — see store.PricingUnset.
		PricePerWorkUnitWei: store.PricingUnset,
		WorkUnit:            "",
		RecipientRand:       r.String(),
		FaceValueWei:        faceValue.String(),
		WinProb:             s.defaultWinProb.String(),
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "open session: %v", err)
	}

	recipientRand, ok := new(big.Int).SetString(sess.RecipientRand, 10)
	if !ok {
		return nil, status.Error(codes.Internal, "session rand corrupt")
	}
	faceValue, ok = new(big.Int).SetString(sess.FaceValueWei, 10)
	if !ok {
		return nil, status.Error(codes.Internal, "session face value corrupt")
	}
	// An explicit request wins over the value stored when the session
	// was first created.
	//
	// "A sender may size its own tickets, but not below the floor" was
	// only true for the FIRST call: afterwards GetOrCreateTicketSession
	// returned the original figure and quietly ignored the request. A
	// sender that needs a larger face value — because a ticket credits
	// its expected value, roughly face/1024, and the payee caps a
	// session at MaxSenderNonces tickets — could never get one, so
	// funding intents above that ceiling were unreachable.
	//
	// Safe to honour: credit is computed from each TICKET's own face
	// value, so tickets already signed keep crediting what they were
	// worth, and a larger face value raises the SENDER's exposure, not
	// this payee's. The floor above still applies.
	if requestedFace != nil {
		faceValue = requestedFace
	}
	winProb, ok := new(big.Int).SetString(sess.WinProb, 10)
	if !ok {
		return nil, status.Error(codes.Internal, "session win prob corrupt")
	}

	return &pb.GetTicketParamsResponse{
		PredecessorWorkId: predecessorWorkID,
		TicketParams: &pb.TicketParams{
			Recipient:         append([]byte(nil), s.recipient...),
			FaceValue:         faceValue.Bytes(),
			WinProb:           winProb.Bytes(),
			RecipientRandHash: types.HashRecipientRand(recipientRand),
			Seed:              ethcommon.LeftPadBytes(recipientRand.Bytes(), 32),
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

// GetRoundRevenue returns confirmed redemption revenue for a single
// Livepeer round.
func (s *Service) GetRoundRevenue(_ context.Context, req *pb.GetRoundRevenueRequest) (*pb.GetRoundRevenueResponse, error) {
	if req.GetRoundId() < 0 {
		return nil, status.Error(codes.InvalidArgument, "round_id must be >= 0")
	}
	revenue, count, err := s.store.RoundRevenue(req.GetRoundId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "round revenue: %v", err)
	}
	return &pb.GetRoundRevenueResponse{
		RoundId:              req.GetRoundId(),
		ConfirmedRevenueWei:  revenue.Bytes(),
		ConfirmedTicketCount: count,
	}, nil
}

// Health returns "ok" — the broker probes this at startup.
func (s *Service) Health(_ context.Context, _ *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Status: "ok"}, nil
}

func mapStoreErr(err error) error {
	if errors.Is(err, store.ErrPricingUnset) {
		return status.Error(codes.FailedPrecondition,
			"session has no offering price; the broker must OpenSession with pricing before billing")
	}
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
