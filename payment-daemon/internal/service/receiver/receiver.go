// Package receiver implements PayeeDaemon — validates incoming payment
// blobs, tracks per-(sender, work_id) balances, and (post chain
// integration) redeems winning tickets via the TicketBroker.
package receiver

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	ethcommon "github.com/ethereum/go-ethereum/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/service/receiver/validator"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/spendauth"
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
	chainID   uint64

	// defaultFaceValue / defaultWinProb size ticket params when the caller
	// does not request a target EV. A target-EV quote retains at least the
	// default face and varies probability. Plan 0016 takes these values at
	// constructor time; future plans can refine per-offering economics.
	defaultFaceValue *big.Int
	defaultWinProb   *big.Int
}

// AdmitAuthorization verifies a payer signature, optionally processes a
// funding batch, and atomically transfers that ticket credit into the stable
// payer-payee account while reserving this authorization's maximum.
func (s *Service) AdmitAuthorization(ctx context.Context, req *pb.AdmitAuthorizationRequest) (*pb.AdmitAuthorizationResponse, error) {
	if len(req.GetAuthorizationBytes()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "authorization_bytes is required")
	}
	var auth pb.SpendAuthorization
	if err := proto.Unmarshal(req.GetAuthorizationBytes(), &auth); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode authorization: %v", err)
	}
	if err := spendauth.Verify(&auth); err != nil {
		return nil, status.Errorf(codes.PermissionDenied, "verify authorization: %v", err)
	}
	payload := auth.GetPayload()
	if !bytes.Equal(payload.GetPayee(), s.recipient) {
		return nil, status.Error(codes.PermissionDenied, "authorization payee does not match receiver")
	}
	if payload.GetChainId() == 0 || (s.chainID != 0 && payload.GetChainId() != s.chainID) || payload.GetDenomination() != "wei" {
		return nil, status.Error(codes.PermissionDenied, "authorization chain or denomination does not match receiver")
	}
	if payload.GetAuthorizationId() == "" || payload.GetRequestId() == "" || payload.GetAcceptedPrice() == nil {
		return nil, status.Error(codes.InvalidArgument, "authorization identity and accepted_price are required")
	}
	if payload.GetBrokerUri() == "" || len(payload.GetRequestDigest()) != sha256.Size {
		return nil, status.Error(codes.InvalidArgument, "authorization broker_uri and SHA-256 request_digest are required")
	}
	if keyLen := len(payload.GetCallerPublicKey()); keyLen != 0 && keyLen != 33 && keyLen != 65 {
		return nil, status.Error(codes.InvalidArgument, "authorization caller_public_key has invalid length")
	}
	switch payload.GetProtocol() {
	case "paid-job/v1":
		if payload.GetSessionId() != "" || payload.GetRevision() != 0 || payload.GetPredecessorAuthorizationId() != "" {
			return nil, status.Error(codes.InvalidArgument, "paid-job/v1 cannot carry session revision fields")
		}
	case "paid-session/v1":
		if payload.GetSessionId() == "" || (payload.GetRevision() == 0) != (payload.GetPredecessorAuthorizationId() == "") {
			return nil, status.Error(codes.InvalidArgument, "paid-session/v1 session and revision fields are inconsistent")
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "authorization protocol is unsupported")
	}
	if payload.GetCapability() != payload.GetAcceptedPrice().GetCapability() || payload.GetOffering() != payload.GetAcceptedPrice().GetOffering() {
		return nil, status.Error(codes.InvalidArgument, "authorization route differs from accepted_price")
	}
	if payload.GetAcceptedPrice().GetUnitsPerPrice() == 0 || payload.GetAcceptedPrice().GetWorkUnitName() == "" {
		return nil, status.Error(codes.InvalidArgument, "authorization price denominator and work unit are required")
	}
	notBefore, err := time.Parse(time.RFC3339Nano, payload.GetNotBefore())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "authorization not_before must be RFC3339")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, payload.GetExpiresAt())
	if err != nil || !expiresAt.After(notBefore) {
		return nil, status.Error(codes.InvalidArgument, "authorization expires_at must be after not_before")
	}
	now := time.Now().UTC()
	if now.Before(notBefore) {
		return nil, status.Error(codes.FailedPrecondition, "authorization is not active yet")
	}
	maxDebit := new(big.Int).SetBytes(payload.GetMaxDebitWei().GetValue())
	price := new(big.Int).SetBytes(payload.GetAcceptedPrice().GetPricePerUnitWei().GetValue())
	if maxDebit.Sign() <= 0 || price.Sign() < 0 || payload.GetMaxTotalUnits() == 0 {
		return nil, status.Error(codes.InvalidArgument, "authorization maximum and price are invalid")
	}
	if required := store.BillFor(price, payload.GetAcceptedPrice().GetUnitsPerPrice(), payload.GetMaxTotalUnits()); required.Cmp(maxDebit) > 0 {
		return nil, status.Error(codes.InvalidArgument, "max_debit_wei cannot cover max_total_units at accepted price")
	}
	fingerprint := sha256.Sum256(req.GetAuthorizationBytes())
	// Admission retries must converge before re-processing an optional ticket.
	// ProcessPayment correctly rejects a nonce replay, so doing it first would
	// turn a lost AdmitAuthorization response into a false payment failure (or,
	// with a freshly-minted retry, duplicate funding). The authorization is the
	// durable idempotency record and therefore wins this race.
	if existing, lookupErr := s.store.GetWholesaleAuthorization(payload.GetPayer(), payload.GetAuthorizationId()); lookupErr == nil {
		if !bytes.Equal(existing.Fingerprint, fingerprint[:]) {
			return nil, status.Error(codes.InvalidArgument, "authorization_id reused with different content")
		}
		if existing.State != store.AuthorizationAdmitted {
			return nil, status.Errorf(codes.FailedPrecondition, "authorization is already %s", existing.State)
		}
		account, accountErr := s.store.GetWholesaleAccount(payload.GetPayer(), payload.GetPayee())
		if accountErr != nil {
			return nil, status.Errorf(codes.Internal, "load wholesale account: %v", accountErr)
		}
		return &pb.AdmitAuthorizationResponse{
			State: authorizationState(existing.State), Account: s.wholesaleAccountView(account),
			ReservedValueWei: &pb.BigUInt{Value: decimalBytes(existing.ReservedWei)},
			CreditedValueWei: &pb.BigUInt{}, Replayed: true,
		}, nil
	} else if !errors.Is(lookupErr, store.ErrAuthorizationNotFound) {
		return nil, status.Errorf(codes.Internal, "lookup spend authorization: %v", lookupErr)
	}

	fundingWorkID := ""
	if len(req.GetPaymentBytes()) > 0 && expiresAt.After(now) {
		var payment pb.Payment
		if err := proto.Unmarshal(req.GetPaymentBytes(), &payment); err != nil || payment.GetTicketParams() == nil {
			return nil, status.Error(codes.InvalidArgument, "funding payment is malformed")
		}
		if !bytes.Equal(payment.GetSender(), payload.GetPayer()) {
			return nil, status.Error(codes.PermissionDenied, "funding payment sender does not match authorization payer")
		}
		fundingWorkID = hex.EncodeToString(payment.GetTicketParams().GetRecipientRandHash())
		processed, err := s.ProcessPayment(ctx, &pb.ProcessPaymentRequest{PaymentBytes: req.GetPaymentBytes(), WorkId: fundingWorkID})
		if err != nil {
			return nil, err
		}
		// NONCE_REPLAY is the expected recovery signal if the daemon
		// credited the batch and crashed before atomically migrating that
		// session balance into the account. AdmitWholesale below can still
		// recover the already-credited balance; every other all-rejected
		// batch is a real refusal.
		allRejected := processed.GetTicketsRejected() == int32(len(payment.GetTicketSenderParams()))
		if len(payment.GetTicketSenderParams()) == 0 || (allRejected && processed.GetDominantRejection() != pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_NONCE_REPLAY) {
			return nil, status.Error(codes.FailedPrecondition, "funding payment credited no tickets")
		}
	}
	reservation := new(big.Int).SetBytes(req.GetReservationValueWei().GetValue())
	result, err := s.store.AdmitWholesale(store.WholesaleAuthorizationSeed{
		ID: payload.GetAuthorizationId(), Fingerprint: fingerprint[:], Payer: payload.GetPayer(), Payee: payload.GetPayee(),
		RequestID: payload.GetRequestId(), SessionID: payload.GetSessionId(), Protocol: payload.GetProtocol(),
		Capability: payload.GetCapability(), Offering: payload.GetOffering(), PriceWei: price.String(),
		PerUnits: payload.GetAcceptedPrice().GetUnitsPerPrice(), WorkUnit: payload.GetAcceptedPrice().GetWorkUnitName(),
		MaxDebitWei: maxDebit.String(), MaxTotalUnits: payload.GetMaxTotalUnits(), ExpiresAt: expiresAt,
		Revision: payload.GetRevision(), PredecessorID: payload.GetPredecessorAuthorizationId(),
	}, fundingWorkID, reservation, now)
	if errors.Is(err, store.ErrInsufficientWholesale) {
		return nil, status.Errorf(codes.FailedPrecondition, "insufficient wholesale account balance: available=%s required=%s", result.Account.Available(), maxDebit)
	}
	if errors.Is(err, store.ErrAuthorizationFingerprint) {
		return nil, status.Error(codes.InvalidArgument, "authorization_id reused with different content")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "admit authorization: %v", err)
	}
	if result.Authorization.State == store.AuthorizationExpiredUnused {
		return nil, status.Error(codes.FailedPrecondition, "authorization expired unused")
	}
	s.recordWholesaleTotals()
	return &pb.AdmitAuthorizationResponse{
		State: authorizationState(result.Authorization.State), Account: s.wholesaleAccountView(result.Account),
		ReservedValueWei: &pb.BigUInt{Value: decimalBytes(result.Authorization.ReservedWei)}, CreditedValueWei: &pb.BigUInt{Value: result.Transferred.Bytes()},
		Replayed: result.Replayed,
	}, nil
}

func (s *Service) AdvanceAuthorization(ctx context.Context, req *pb.AdvanceAuthorizationRequest) (*pb.AdvanceAuthorizationResponse, error) {
	if len(req.GetPayer()) != 20 || req.GetAuthorizationId() == "" || req.GetAdvanceSeq() == 0 {
		return nil, status.Error(codes.InvalidArgument, "payer, authorization_id, and positive advance_seq are required")
	}
	// As with admission, the durable sequence is checked before an optional
	// payment is processed. A retry of an already-applied advance must not
	// replay a ticket nonce or entice the payer to mint a replacement ticket.
	existing, lookupErr := s.store.GetWholesaleAuthorization(req.GetPayer(), req.GetAuthorizationId())
	if errors.Is(lookupErr, store.ErrAuthorizationNotFound) {
		return nil, status.Error(codes.NotFound, "spend authorization not found")
	}
	if lookupErr != nil {
		return nil, status.Errorf(codes.Internal, "lookup spend authorization: %v", lookupErr)
	}
	fundingWorkID := ""
	isReplay := req.GetAdvanceSeq() <= existing.SettlementSeq
	if len(req.GetPaymentBytes()) > 0 && !isReplay {
		var payment pb.Payment
		if err := proto.Unmarshal(req.GetPaymentBytes(), &payment); err != nil || payment.GetTicketParams() == nil {
			return nil, status.Error(codes.InvalidArgument, "funding payment is malformed")
		}
		if !bytes.Equal(payment.GetSender(), req.GetPayer()) {
			return nil, status.Error(codes.PermissionDenied, "funding payment sender does not match authorization payer")
		}
		fundingWorkID = hex.EncodeToString(payment.GetTicketParams().GetRecipientRandHash())
		processed, err := s.ProcessPayment(ctx, &pb.ProcessPaymentRequest{PaymentBytes: req.GetPaymentBytes(), WorkId: fundingWorkID})
		if err != nil {
			return nil, err
		}
		allRejected := processed.GetTicketsRejected() == int32(len(payment.GetTicketSenderParams()))
		if len(payment.GetTicketSenderParams()) == 0 || (allRejected && processed.GetDominantRejection() != pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_NONCE_REPLAY) {
			return nil, status.Error(codes.FailedPrecondition, "funding payment credited no tickets")
		}
	}
	target := new(big.Int).SetBytes(req.GetTargetReservedValueWei().GetValue())
	result, err := s.store.AdvanceWholesale(req.GetPayer(), s.recipient, req.GetAuthorizationId(), req.GetCumulativeUnits(), target, req.GetAdvanceSeq(), fundingWorkID, time.Now().UTC())
	switch {
	case errors.Is(err, store.ErrInsufficientWholesale):
		return nil, status.Error(codes.FailedPrecondition, "insufficient wholesale account balance for requested runway")
	case errors.Is(err, store.ErrAuthorizationNotFound):
		return nil, status.Error(codes.NotFound, "spend authorization not found")
	case errors.Is(err, store.ErrAuthorizationState), errors.Is(err, store.ErrAuthorizationSettlement):
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	case err != nil:
		return nil, status.Errorf(codes.Internal, "advance authorization: %v", err)
	}
	s.recordWholesaleTotals()
	return &pb.AdvanceAuthorizationResponse{State: authorizationState(result.Authorization.State), Account: s.wholesaleAccountView(result.Account), BilledDeltaWei: &pb.BigUInt{Value: result.BilledDelta.Bytes()}, CumulativeBilledValueWei: &pb.BigUInt{Value: decimalBytes(result.Authorization.BilledWei)}, ReservedValueWei: &pb.BigUInt{Value: decimalBytes(result.Authorization.ReservedWei)}, CreditedValueWei: &pb.BigUInt{Value: result.Transferred.Bytes()}, Replayed: result.Replayed}, nil
}

func (s *Service) SettleAuthorization(_ context.Context, req *pb.SettleAuthorizationRequest) (*pb.SettleAuthorizationResponse, error) {
	if len(req.GetPayer()) != 20 || req.GetAuthorizationId() == "" || req.GetSettlementSeq() == 0 {
		return nil, status.Error(codes.InvalidArgument, "payer, authorization_id, and positive settlement_seq are required")
	}
	result, err := s.store.SettleWholesale(req.GetPayer(), s.recipient, req.GetAuthorizationId(), req.GetActualUnits(), req.GetSettlementSeq(), time.Now().UTC())
	switch {
	case errors.Is(err, store.ErrAuthorizationNotFound):
		return nil, status.Error(codes.NotFound, "spend authorization not found")
	case errors.Is(err, store.ErrAuthorizationSettlement):
		return nil, status.Error(codes.InvalidArgument, "settlement replay differs from recorded content")
	case errors.Is(err, store.ErrAuthorizationState):
		return nil, status.Error(codes.FailedPrecondition, "authorization cannot settle these units")
	case err != nil:
		return nil, status.Errorf(codes.Internal, "settle authorization: %v", err)
	}
	s.recordWholesaleTotals()
	return &pb.SettleAuthorizationResponse{
		State: authorizationState(result.Authorization.State), Account: s.wholesaleAccountView(result.Account),
		BilledValueWei: &pb.BigUInt{Value: result.Billed.Bytes()}, ReleasedValueWei: &pb.BigUInt{Value: result.Released.Bytes()},
		Replayed: result.Replayed,
	}, nil
}

func (s *Service) FundWholesaleAccount(ctx context.Context, req *pb.FundWholesaleAccountRequest) (*pb.FundWholesaleAccountResponse, error) {
	var payment pb.Payment
	if err := proto.Unmarshal(req.GetPaymentBytes(), &payment); err != nil || payment.GetTicketParams() == nil || len(payment.GetSender()) != 20 || len(payment.GetTicketSenderParams()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "funding payment is malformed")
	}
	workID := hex.EncodeToString(payment.GetTicketParams().GetRecipientRandHash())
	processed, err := s.ProcessPayment(ctx, &pb.ProcessPaymentRequest{PaymentBytes: req.GetPaymentBytes(), WorkId: workID})
	if err != nil {
		return nil, err
	}
	allRejected := processed.GetTicketsRejected() == int32(len(payment.GetTicketSenderParams()))
	if allRejected && processed.GetDominantRejection() != pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_NONCE_REPLAY {
		return nil, status.Error(codes.FailedPrecondition, "funding payment credited no tickets")
	}
	account, transferred, err := s.store.FundWholesale(payment.GetSender(), s.recipient, workID, time.Now().UTC())
	if errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.FailedPrecondition, "funding payment session not found")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "fund wholesale account: %v", err)
	}
	s.recordWholesaleTotals()
	return &pb.FundWholesaleAccountResponse{
		Account: s.wholesaleAccountView(account), CreditedValueWei: &pb.BigUInt{Value: transferred.Bytes()},
		Replayed: allRejected && processed.GetDominantRejection() == pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_NONCE_REPLAY,
	}, nil
}

func (s *Service) GetWholesaleAccount(_ context.Context, req *pb.GetWholesaleAccountRequest) (*pb.GetWholesaleAccountResponse, error) {
	if len(req.GetPayer()) != 20 {
		return nil, status.Error(codes.InvalidArgument, "payer must be 20 bytes")
	}
	account, err := s.store.GetWholesaleAccount(req.GetPayer(), s.recipient)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get wholesale account: %v", err)
	}
	return &pb.GetWholesaleAccountResponse{Account: s.wholesaleAccountView(account)}, nil
}

func (s *Service) GetSpendAuthorization(_ context.Context, req *pb.GetSpendAuthorizationRequest) (*pb.GetSpendAuthorizationResponse, error) {
	auth, err := s.store.GetWholesaleAuthorization(req.GetPayer(), req.GetAuthorizationId())
	if errors.Is(err, store.ErrAuthorizationNotFound) {
		return nil, status.Error(codes.NotFound, "spend authorization not found")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get spend authorization: %v", err)
	}
	return &pb.GetSpendAuthorizationResponse{
		State: authorizationState(auth.State), ReservedValueWei: &pb.BigUInt{Value: decimalBytes(auth.ReservedWei)},
		BilledValueWei:   &pb.BigUInt{Value: decimalBytes(auth.BilledWei)},
		ReleasedValueWei: &pb.BigUInt{Value: decimalBytes(auth.ReleasedWei)},
		ActualUnits:      auth.ActualUnits, SettlementSeq: auth.SettlementSeq, ObservedAt: auth.UpdatedAt.Format(time.RFC3339Nano),
	}, nil
}

func authorizationState(state string) pb.SpendAuthorizationState {
	switch state {
	case store.AuthorizationAdmitted:
		return pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED
	case store.AuthorizationSettled:
		return pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED
	case store.AuthorizationExpiredUnused:
		return pb.SpendAuthorizationState_SPEND_AUTHORIZATION_EXPIRED_UNUSED
	case store.AuthorizationSuperseded:
		return pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SUPERSEDED
	default:
		return pb.SpendAuthorizationState_SPEND_AUTHORIZATION_STATE_UNSPECIFIED
	}
}

func (s *Service) wholesaleAccountView(account *store.WholesaleAccount) *pb.WholesaleAccountView {
	return &pb.WholesaleAccountView{
		Payer: append([]byte(nil), account.Payer...), Payee: append([]byte(nil), account.Payee...),
		CreditedValueWei:  &pb.BigUInt{Value: decimalBytes(account.CreditedWei)},
		ReservedValueWei:  &pb.BigUInt{Value: decimalBytes(account.ReservedWei)},
		DebitedValueWei:   &pb.BigUInt{Value: decimalBytes(account.DebitedWei)},
		AvailableValueWei: &pb.BigUInt{Value: account.Available().Bytes()}, Version: account.Version,
		ObservedAt: account.UpdatedAt.Format(time.RFC3339Nano), ChainId: s.chainID, Denomination: "wei",
	}
}

func decimalBytes(value string) []byte {
	n, ok := new(big.Int).SetString(value, 10)
	if !ok || n.Sign() <= 0 {
		return nil
	}
	return n.Bytes()
}

func (s *Service) recordWholesaleTotals() {
	totals, err := s.store.GetWholesaleTotals()
	if err != nil || totals == nil {
		return
	}
	s.metrics.SetWholesaleAccountTotals(
		metrics.WeiToFloat(decimalBig(totals.CreditedWei)),
		metrics.WeiToFloat(decimalBig(totals.ReservedWei)),
		metrics.WeiToFloat(decimalBig(totals.DebitedWei)),
		metrics.WeiToFloat(totals.Available()),
	)
}

func decimalBig(value string) *big.Int {
	n, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return new(big.Int)
	}
	return n
}

// Config holds the receiver service's tunable state.
type Config struct {
	// Recipient is the 20-byte ETH address this daemon receives as.
	// Derived at boot from the keystore (or the --orch-address override
	// for hot/cold split).
	Recipient []byte
	ChainID   uint64

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
		chainID:          cfg.ChainID,
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
	winProb := new(big.Int).Set(s.defaultWinProb)
	var requestedEV *big.Int
	if got := req.GetFaceValue(); len(got) > 0 {
		requestedEV = new(big.Int).SetBytes(got)
		// This wire field retains its historical name, but its contract is
		// target expected value. Preserve a redeemable winning face and vary
		// probability: shrinking face value for a small account refill merely
		// moves stranded value from payer to payee.
		if requestedEV.Sign() <= 0 {
			return nil, status.Error(codes.InvalidArgument, "target expected value must be positive")
		}
		if requestedEV.Cmp(faceValue) > 0 {
			faceValue.Set(requestedEV)
		}
		winProb = winProbForExactEV(faceValue, requestedEV)
		if winProb.Sign() <= 0 || winProb.Cmp(types.MaxWinProb) > 0 || types.CreditedEV(faceValue, winProb).Cmp(requestedEV) != 0 {
			return nil, status.Errorf(codes.FailedPrecondition,
				"cannot represent target expected value %s with redeemable face value %s", requestedEV, faceValue)
		}
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
				// Conditional on the session actually observed above.
				//
				// The lookup, the count and the reset are three separate
				// reads, so another caller can rotate between them. An
				// unconditional reset would then retire the SUCCESSOR
				// that caller just created, leaving the tuple with no
				// live identity — the compare-and-swap makes this a
				// no-op in exactly that case, and the caller that did
				// rotate keeps its successor.
				rotated, rerr := s.store.ResetTicketSessionIfCurrent(tupleKey, existing.WorkID)
				if rerr != nil {
					return nil, status.Errorf(codes.Internal, "rotate exhausted session: %v", rerr)
				}
				if rotated {
					predecessorWorkID = existing.WorkID
					s.logger.Info("ticket session rotated: nonce budget exhausted",
						"predecessor_work_id", existing.WorkID,
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
		WinProb:             winProb.String(),
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
	// An explicit target wins over the sizing stored when the session was
	// first created.
	//
	// GetOrCreateTicketSession returns the original figures. Reusing those
	// figures for a later, differently-sized refill silently transfers the
	// old EV. Recompute both economic knobs while retaining the stable rand.
	//
	// Safe to honour: each ticket carries its own face/probability, and the
	// receiver validates and credits those exact signed values.
	if requestedEV != nil {
		faceValue = new(big.Int).Set(s.defaultFaceValue)
		if requestedEV.Cmp(faceValue) > 0 {
			faceValue.Set(requestedEV)
		}
		winProb = winProbForExactEV(faceValue, requestedEV)
	} else {
		winProb, ok = new(big.Int).SetString(sess.WinProb, 10)
		if !ok {
			return nil, status.Error(codes.Internal, "session win prob corrupt")
		}
	}

	// State what this payee has already recorded against the rand, so a
	// sender whose own counter was lost resumes above it instead of
	// replaying into rejections it cannot diagnose.
	highestSeen, hasSeen, hErr := s.store.HighestSenderNonce(recipientRand)
	if hErr != nil {
		return nil, status.Errorf(codes.Internal, "read nonce high-water mark: %v", hErr)
	}

	return &pb.GetTicketParamsResponse{
		PredecessorWorkId: predecessorWorkID,
		HighestSeenNonce:  highestSeen,
		HasSeenNonces:     hasSeen,
		TicketParams: &pb.TicketParams{
			Recipient:         append([]byte(nil), s.recipient...),
			FaceValue:         faceValue.Bytes(),
			WinProb:           winProb.Bytes(),
			RecipientRandHash: types.HashRecipientRand(recipientRand),
			Seed:              ethcommon.LeftPadBytes(recipientRand.Bytes(), 32),
		},
	}, nil
}

// winProbForExactEV returns the smallest probability whose integer expected
// value at faceValue reaches target. Callers keep faceValue >= target, so the
// result is at most MaxWinProb and CreditedEV lands exactly on target.
func winProbForExactEV(faceValue, target *big.Int) *big.Int {
	numerator := new(big.Int).Mul(new(big.Int).Set(target), types.MaxWinProb)
	prob, rem := new(big.Int).QuoRem(numerator, faceValue, new(big.Int))
	if rem.Sign() != 0 {
		prob.Add(prob, big.NewInt(1))
	}
	return prob
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
