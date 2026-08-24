// Package sender implements the PayerDaemon RPC surface and the
// sender-side ticket-creation state machine.
//
// Current scope:
//   - CreatePayment fetches quote-free payee-issued TicketParams over
//     HTTP from the broker's `/v1/payment/ticket-params` endpoint.
//   - The sender caches sessions by (recipient, capability, offering,
//     requested funded value, ticket-params base URL) so repeated calls
//     reuse the same recipient_rand_hash and nonce stream.
//   - Accepted quote metadata is refreshed on every CreatePayment call
//     even when the nonce stream is reused from an existing session.
//   - Each payment is signed against the authoritative TicketParams
//     returned by the payee, not against a locally fabricated copy.
package sender

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math/big"
	"net/url"
	"strings"
	"sync"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/store"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/types"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

// maxTicketsPerPayment bounds a single minted batch.
//
// Tied to the payee's per-session nonce budget, because that is the real
// constraint: the payee rejects tickets past store.MaxSenderNonces on a
// session and credits only what it accepted, so minting more than the
// budget silently under-funds. LOC hit exactly that — 601 tickets, the
// last one rejected, 613,975 credited of 616,025 requested.
//
// After rescaling a session's face value this should always be 1. A
// count above the budget means the rescale did not take, and refusing is
// the honest answer: the caller learns now rather than discovering it as
// insufficient_balance after the exchange was admitted.
const maxTicketsPerPayment = store.MaxSenderNonces

// Service implements pb.PayerDaemonServer.
type Service struct {
	pb.UnimplementedPayerDaemonServer

	keystore providers.KeyStore
	broker   providers.Broker
	clock    providers.Clock
	logger   *slog.Logger
	fetcher  TicketParamsFetcher
	metrics  metrics.Recorder

	// validityPeriod caches the contract's ticketValidityPeriod for a
	// short window. NOT for the process lifetime: it is governance
	// settable, and a value cached at startup is a value that can be
	// wrong for as long as the daemon runs.
	validityMu     sync.Mutex
	validityPeriod int64
	validityReadAt time.Time

	// limits is this payer's own policy on what it will sign. Enforced
	// before any signature, because a refusal after signing is not a
	// refusal.
	limits Limits

	// store is the durable mint-idempotency ledger. Minting signs
	// tickets against real deposit, so a retry after an uncertain
	// response must replay rather than re-sign, and that record has to
	// outlive the process.
	store *store.Store

	// mintMu serializes concurrent CreatePayment calls sharing a mint id.
	// The durable reservation makes a crash safe; this makes a RACE
	// safe — without it two simultaneous identical requests both see an
	// unreserved id and both sign.
	mintMu sync.Map // mint key -> *sync.Mutex

	mu          sync.Mutex
	sessions    map[string]*senderSession // keyed by recipient/capability/offering/target-spend tuple
	workIDIndex map[string]string         // work_id -> session cache key
}

type senderSession struct {
	workID        string
	cacheKey      string
	ticketParams  *types.TicketParams
	nonce         uint32
	acceptedPrice *types.PriceInfo
	acceptedQuote *pb.QuoteRef
	capability    string
	offering      string
}

// New constructs a sender Service. rec may be nil (no-op metrics).
func New(keystore providers.KeyStore, broker providers.Broker, clock providers.Clock, logger *slog.Logger, fetcher TicketParamsFetcher, rec metrics.Recorder, st *store.Store, limits Limits) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if rec == nil {
		rec = metrics.NewNoop()
	}
	svc := &Service{
		keystore:    keystore,
		broker:      broker,
		clock:       clock,
		logger:      logger,
		fetcher:     fetcher,
		metrics:     rec,
		store:       st,
		limits:      limits,
		sessions:    map[string]*senderSession{},
		workIDIndex: map[string]string{},
	}
	return svc
}

// CreatePayment implements pb.PayerDaemonServer.
func (s *Service) CreatePayment(ctx context.Context, req *pb.CreatePaymentRequest) (resp *pb.CreatePaymentResponse, err error) {
	defer func() {
		if err != nil {
			s.metrics.IncPaymentCreated(metrics.ResultError)
		} else {
			s.metrics.IncPaymentCreated(metrics.ResultOK)
		}
	}()
	if len(req.GetRecipient()) == 0 {
		return nil, errors.New("recipient is empty")
	}
	mintID := strings.TrimSpace(req.GetMintRequestId())
	if mintID == "" {
		return nil, grpcstatus.Error(codes.InvalidArgument, "mint_request_id is required")
	}
	if len(mintID) > maxMintRequestIDBytes {
		return nil, grpcstatus.Errorf(codes.InvalidArgument,
			"mint_request_id exceeds %d bytes", maxMintRequestIDBytes)
	}
	if s.store == nil {
		// Without the ledger there is no way to keep the idempotency
		// promise, and minting anyway would be the unsafe half of it.
		return nil, grpcstatus.Error(codes.FailedPrecondition,
			"mint idempotency store is not configured; refusing to mint")
	}
	fingerprint := mintFingerprint(req)

	// Serialize this id, then reserve it durably before anything is
	// signed. The two together give exactly-once across both the racing
	// case and the crashing one.
	unlock := s.lockMint(mintID)
	defer unlock()

	prior, err := s.store.MintReserve(s.keystore.Address(), mintID, fingerprint)
	switch {
	case errors.Is(err, store.ErrMintIncomplete):
		return nil, grpcstatus.Error(codes.FailedPrecondition,
			"mint_request_id was reserved but never completed; a ticket may already have been signed for it — use a new id")
	case errors.Is(err, store.ErrMintFingerprintMismatch):
		return nil, grpcstatus.Error(codes.InvalidArgument,
			"mint_request_id was used for different request content")
	case errors.Is(err, store.ErrMintExpired):
		// Deliberately not a fresh mint. The replay record aged out, but
		// the key was issued a payment once, and re-minting it now would
		// pay twice for one intent.
		return nil, grpcstatus.Error(codes.FailedPrecondition,
			"mint_request_id was already used and its replay record has expired; use a new id")
	case err != nil:
		return nil, grpcstatus.Errorf(codes.Internal, "mint recall: %v", err)
	case prior != nil:
		s.metrics.IncPaymentCreated(metrics.ResultOK)
		return mintResponseFrom(prior), nil
	}

	acceptedPrice, err := parseAcceptedPrice(req.GetAcceptedPrice())
	if err != nil {
		return nil, fmt.Errorf("accepted_price: %w", err)
	}
	funding, err := parseFundingIntent(req.GetFunding())
	if err != nil {
		return nil, fmt.Errorf("funding: %w", err)
	}
	// Policy check before any signing, minting or network call.
	if err := s.limits.CheckMint(acceptedPrice.WorkUnitName,
		big.NewInt(acceptedPrice.PricePerUnitWei),
		acceptedPrice.UnitsPerPrice, funding.fundedValueWei); err != nil {
		s.logger.Warn("refusing mint: spend limit", "err", err.Error())
		return nil, grpcstatus.Errorf(codes.FailedPrecondition, "spend limit: %v", err)
	}
	if s.fetcher == nil {
		return nil, errors.New("ticket params fetcher is not configured")
	}
	if strings.TrimSpace(req.GetTicketParamsBaseUrl()) == "" {
		return nil, errors.New("ticket params base URL is empty")
	}

	// Defense-in-depth sender validation — query Broker for
	// deposit/reserve. Dev fake always returns "fine"; chain-backed
	// sender mode rejects on no-deposit / pending-unlock.
	info, err := s.broker.GetSenderInfo(ctx, s.keystore.Address())
	if err != nil {
		return nil, fmt.Errorf("get sender info: %w", err)
	}
	s.recordSenderFunds(info)
	if err := validateSenderInfo(info, s.clock.LastInitializedRound()); err != nil {
		return nil, fmt.Errorf("sender validation: %w", err)
	}

	// Read the expiry parameter BEFORE signing anything.
	//
	// Fail-closed on purpose: every envelope this daemon issues carries a
	// deadline derived from this number, and a consumer makes release
	// decisions against it. A daemon that cannot read it has nothing
	// honest to publish, and signing anyway would hand out an envelope
	// with a deadline we made up.
	validityPeriod, validityObservedAt, err := s.ticketValidityPeriod(ctx)
	if err != nil {
		return nil, grpcstatus.Errorf(codes.Unavailable,
			"cannot read the chain's ticketValidityPeriod, so this mint would carry an "+
				"unverified expiry deadline; refusing: %v", err)
	}

	session, err := s.findOrOpenSession(
		ctx,
		req.GetRecipient(),
		funding.fundedValueWei,
		acceptedPrice.CapabilityName,
		acceptedPrice.Offering,
		req.GetTicketParamsBaseUrl(),
		acceptedPrice.toPriceInfo(funding.estimatedUnits),
		req.GetAcceptedPrice().GetQuoteRef(),
	)
	if err != nil {
		return nil, fmt.Errorf("ticket params: %w", err)
	}
	if len(session.ticketParams.Seed) == 0 {
		return nil, errors.New("ticket params: seed is empty")
	}

	// Size the batch so the payment actually funds what was asked for.
	//
	// A ticket credits its EXPECTED value, not its face value:
	// floor(face x win_prob / MaxWinProb), which at the default win
	// probability is about face/1024. One ticket was minted regardless
	// of funded_value_wei, so a 3,000 wei intent bought 2 wei of credit
	// and a caller funded 512x less than it believed. The face value
	// cannot be raised to fix this — the payee fixes it when the session
	// opens, and a resize must not move work_id — so the batch grows in
	// TICKETS instead. Same session, same face value, N times the credit.
	perTicketEV := types.CreditedEV(session.ticketParams.FaceValue, session.ticketParams.WinProb)
	if perTicketEV.Sign() <= 0 {
		// Nothing this session can mint credits anything. Failing closed
		// beats handing back a payment that funds no work: the caller
		// would discover it as insufficient_balance after the exchange
		// had already been admitted.
		return nil, grpcstatus.Errorf(codes.FailedPrecondition,
			"this ticket session credits 0 wei per ticket (face_value %s, win_prob %s), "+
				"so no number of tickets can fund %s wei",
			session.ticketParams.FaceValue, session.ticketParams.WinProb, funding.fundedValueWei)
	}
	ticketCount := new(big.Int).Add(
		new(big.Int).Quo(funding.fundedValueWei, perTicketEV),
		big.NewInt(1))
	if new(big.Int).Mod(funding.fundedValueWei, perTicketEV).Sign() == 0 {
		ticketCount.Sub(ticketCount, big.NewInt(1))
	}
	if ticketCount.Sign() <= 0 {
		ticketCount = big.NewInt(1)
	}
	if ticketCount.Cmp(big.NewInt(maxTicketsPerPayment)) > 0 {
		// Refuse rather than silently under-funding. Every ticket is
		// independently redeemable at face value, so a batch this large
		// is also an exposure the caller has not been shown.
		return nil, grpcstatus.Errorf(codes.FailedPrecondition,
			"funding %s wei at %s wei of credit per ticket needs %s tickets, above the %d cap; "+
				"raise the offering's face value or fund less per payment",
			funding.fundedValueWei, perTicketEV, ticketCount, maxTicketsPerPayment)
	}
	n := int(ticketCount.Int64())

	tsps := make([]*types.TicketSenderParams, 0, n)
	for i := 0; i < n; i++ {
		tsp, err := s.signOneTicket(session)
		if err != nil {
			return nil, fmt.Errorf("sign ticket %d/%d: %w", i+1, n, err)
		}
		tsps = append(tsps, tsp)
	}
	tsp := tsps[0]

	batch := &types.TicketBatch{
		TicketParams: session.ticketParams,
		Sender:       s.keystore.Address(),
		ExpirationParams: &types.TicketExpirationParams{
			CreationRound:          s.clock.LastInitializedRound(),
			CreationRoundBlockHash: s.clock.LastInitializedL1BlockHash(),
		},
		TicketSenderParams: tsps,
		ExpectedPrice:      session.acceptedPrice,
	}
	wire := batch.ToWirePayment()
	bytes, err := proto.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("marshal payment: %w", err)
	}

	// What the payee will credit for this batch, computed the way the
	// payee computes it: per-ticket integer EV, summed. Not the rational
	// face x win / 2^256, which rounds differently and would report a
	// value the ledger never credits.
	totalEV := new(big.Int).Mul(perTicketEV, ticketCount)
	evBytes := totalEV.Bytes()

	s.logger.Info("payment created",
		"work_id", session.workID,
		"capability", acceptedPrice.CapabilityName,
		"offering", acceptedPrice.Offering,
		"funded_value_wei", funding.fundedValueWei.String(),
		"ticket_face_value", session.ticketParams.FaceValue.String(),
		"tickets", n,
		"expected_value_wei", totalEV.String(),
		"first_nonce", tsp.SenderNonce)

	out := &pb.CreatePaymentResponse{
		PaymentBytes:     bytes,
		TicketsCreated:   uint32(n),
		ExpectedValue:    &pb.BigUInt{Value: evBytes},
		FundedValueWei:   &pb.BigUInt{Value: funding.fundedValueWei.Bytes()},
		AcceptedQuoteRef: cloneQuoteRef(session.acceptedQuote),
		WorkId:           session.workID,
		// When this envelope dies UNDER THE PARAMETER OBSERVED NOW.
		//
		// Not a property of the envelope: the contract evaluates
		// creationRound + ticketValidityPeriod > currentRound against
		// CURRENT storage, and keeps round block hashes forever, so
		// raising the parameter extends tickets already issued and can
		// revive lapsed ones. The period is published alongside so a
		// consumer can compare it to the chain and notice.
		//
		// The last redeemable round is creationRound + period - 1, which
		// is what the contract's strict > comparison works out to.
		CreationRound:                  batch.ExpirationParams.CreationRound,
		ExpiresAfterRound:              batch.ExpirationParams.CreationRound + validityPeriod - 1,
		TicketValidityPeriod:           validityPeriod,
		TicketValidityPeriodObservedAt: validityObservedAt.Format(time.RFC3339Nano),
	}
	// Record before returning. A crash between the signature and this
	// write leaves the ticket minted and unrecorded, and the retry
	// re-mints — so the window is narrowed to a single write rather than
	// closed. Closing it entirely would need the nonce reservation and
	// the record in one transaction, which is the next increment.
	quoteJSON, err := marshalQuoteRef(out.AcceptedQuoteRef)
	if err != nil {
		return nil, fmt.Errorf("record mint: %w", err)
	}
	if err := s.store.MintRecord(s.keystore.Address(), mintID, store.MintRecord{
		Fingerprint:    fingerprint,
		PaymentBytes:   out.PaymentBytes,
		TicketsCreated: out.TicketsCreated,
		ExpectedValue:  evBytes,
		FundedValueWei: funding.fundedValueWei.Bytes(),
		QuoteRefJSON:   quoteJSON,
		WorkID:         out.WorkId,
	}); err != nil {
		return nil, grpcstatus.Errorf(codes.Internal, "record mint: %v", err)
	}
	return out, nil
}

// lockMint serializes CreatePayment calls sharing a mint id, returning
// the unlock. Keyed per id rather than one global lock so unrelated
// mints stay concurrent.
func (s *Service) lockMint(mintID string) func() {
	actual, _ := s.mintMu.LoadOrStore(mintID, &sync.Mutex{})
	mu := actual.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// maxMintRequestIDBytes caps the caller-supplied key. Long enough for a
// prefixed UUID, short enough that the tombstone space stays predictable.
const maxMintRequestIDBytes = 128

// mintFingerprint binds a mint id to the request it paid for. A reused
// id with different content is refused rather than answered with the
// earlier payment, which would hand the caller a batch it never asked
// for.
func mintFingerprint(req *pb.CreatePaymentRequest) []byte { return MintFingerprint(req) }

// MintFingerprint is the content a mint id promises. Exported because it
// is part of the idempotency contract — "the same id with the same
// content replays" is only meaningful if a caller can see what content
// means — and because tests reconstruct it to simulate a crash.
func MintFingerprint(req *pb.CreatePaymentRequest) []byte {
	h := sha256.New()
	h.Write(req.GetRecipient())
	h.Write([]byte{0})
	h.Write([]byte(strings.TrimSpace(req.GetTicketParamsBaseUrl())))
	h.Write([]byte{0})
	if p, err := proto.Marshal(req.GetAcceptedPrice()); err == nil {
		h.Write(p)
	}
	h.Write([]byte{0})
	if f, err := proto.Marshal(req.GetFunding()); err == nil {
		h.Write(f)
	}
	return h.Sum(nil)
}

func mintResponseFrom(rec *store.MintRecord) *pb.CreatePaymentResponse {
	out := &pb.CreatePaymentResponse{
		PaymentBytes:   rec.PaymentBytes,
		TicketsCreated: rec.TicketsCreated,
		WorkId:         rec.WorkID,
	}
	if len(rec.ExpectedValue) > 0 {
		out.ExpectedValue = &pb.BigUInt{Value: rec.ExpectedValue}
	}
	if len(rec.FundedValueWei) > 0 {
		out.FundedValueWei = &pb.BigUInt{Value: rec.FundedValueWei}
	}
	if len(rec.QuoteRefJSON) > 0 {
		var qr pb.QuoteRef
		if err := protojson.Unmarshal(rec.QuoteRefJSON, &qr); err == nil {
			out.AcceptedQuoteRef = &qr
		}
	}
	return out
}

func marshalQuoteRef(qr *pb.QuoteRef) ([]byte, error) {
	if qr == nil {
		return nil, nil
	}
	return protojson.Marshal(qr)
}

// ReportPaymentResult applies payee-side feedback to sender session state.
func (s *Service) ReportPaymentResult(_ context.Context, req *pb.ReportPaymentResultRequest) (*pb.ReportPaymentResultResponse, error) {
	if strings.TrimSpace(req.GetWorkId()) == "" {
		return nil, errors.New("work_id is empty")
	}
	if req.GetRejectionReason() != pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_INVALID_RECIPIENT_RAND {
		return &pb.ReportPaymentResultResponse{}, nil
	}

	evicted := s.evictSessionByWorkID(req.GetWorkId())
	s.logger.Warn("sender session invalidated from payee rejection",
		"work_id", req.GetWorkId(),
		"capability", req.GetCapability(),
		"offering", req.GetOffering(),
		"rejection_reason", req.GetRejectionReason().String(),
		"evicted", evicted)

	st := grpcstatus.New(codes.Aborted, "payment session rotated; retry exactly once")
	withDetails, err := st.WithDetails(
		&errdetails.ErrorInfo{
			Reason: "INVALID_RECIPIENT_RAND",
			Domain: "payments.livepeer.org",
			Metadata: map[string]string{
				"old_work_id": req.GetWorkId(),
				"capability":  req.GetCapability(),
				"offering":    req.GetOffering(),
			},
		},
		&errdetails.RetryInfo{RetryDelay: durationpb.New(0)},
	)
	if err != nil {
		return nil, st.Err()
	}
	return nil, withDetails.Err()
}

// GetDepositInfo implements pb.PayerDaemonServer.
func (s *Service) GetDepositInfo(ctx context.Context, _ *pb.GetDepositInfoRequest) (*pb.GetDepositInfoResponse, error) {
	info, err := s.broker.GetSenderInfo(ctx, s.keystore.Address())
	if err != nil {
		return nil, err
	}
	s.recordSenderFunds(info)
	out := &pb.GetDepositInfoResponse{
		WithdrawRound: info.WithdrawRound,
		// The same clock that stamps creation_round on a mint, so a
		// consumer evaluating "has this envelope expired" reads one
		// clock rather than correlating two.
		CurrentRound: s.clock.LastInitializedRound(),
	}
	// The CURRENT period, read fresh. Paired with what a mint recorded,
	// this is how a consumer notices that governance moved a deadline it
	// may already have acted on.
	if v, at, verr := s.ticketValidityPeriodFresh(ctx); verr == nil {
		out.TicketValidityPeriod = v
		out.TicketValidityPeriodObservedAt = at.Format(time.RFC3339Nano)
	} else {
		s.logger.Error("could not read ticketValidityPeriod for deposit info; a consumer cannot "+
			"verify whether recorded expiry deadlines still hold", "err", verr)
	}
	if info.Deposit != nil {
		out.Deposit = info.Deposit.Bytes()
	}
	if info.Reserve != nil && info.Reserve.FundsRemaining != nil {
		out.Reserve = info.Reserve.FundsRemaining.Bytes()
	}
	return out, nil
}

// Health implements pb.PayerDaemonServer.
func (s *Service) Health(_ context.Context, _ *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Status: "ok"}, nil
}

// ─── helpers ──────────────────────────────────────────────────────────

// recordSenderFunds updates the on-chain deposit/reserve gauges.
func (s *Service) recordSenderFunds(info *providers.SenderInfo) {
	if info == nil {
		return
	}
	s.metrics.SetSenderDepositWei(metrics.WeiToFloat(info.Deposit))
	if info.Reserve != nil {
		s.metrics.SetSenderReserveWei(metrics.WeiToFloat(info.Reserve.FundsRemaining))
	}
}

// rescaleTicketParams re-quotes a session at a face value whose
// per-ticket expected value covers `want`, keeping the tuple's recipient
// rand — so work_id does not move and the session is the same one.
//
// A ticket credits floor(face x win_prob / MaxWinProb), and win_prob is
// the payee's to choose, so the face value is the only lever the sender
// has. Scaling it is what makes ONE ticket carry the intent, which
// matters because the payee caps a session at store.MaxSenderNonces
// tickets: paying for a large intent in many small tickets runs out of
// nonces long before it runs out of money.
func (s *Service) rescaleTicketParams(ctx context.Context, recipient []byte, want *big.Int,
	capability, offering, baseURL string, current *types.TicketParams) (*types.TicketParams, error) {

	if current.WinProb == nil || current.WinProb.Sign() <= 0 {
		return nil, grpcstatus.Errorf(codes.FailedPrecondition,
			"payee quoted win_prob %v, so no ticket on this session can credit anything",
			current.WinProb)
	}
	// scaled = ceil(want x MaxWinProb / win_prob): the face value whose
	// per-ticket credit is at least the funding intent.
	scaled, rem := new(big.Int).QuoRem(
		new(big.Int).Mul(want, types.MaxWinProb), current.WinProb, new(big.Int))
	if rem.Sign() != 0 {
		scaled.Add(scaled, big.NewInt(1))
	}

	fetchStart := time.Now()
	retry, err := s.fetcher.Fetch(ctx, TicketParamsRequest{
		BaseURL:    baseURL,
		Sender:     append([]byte(nil), s.keystore.Address()...),
		Recipient:  append([]byte(nil), recipient...),
		FaceValue:  scaled,
		Capability: capability,
		Offering:   offering,
	})
	s.metrics.ObserveTicketParamsFetch(time.Since(fetchStart))
	if err != nil {
		s.metrics.IncTicketParamsFetch(metrics.ResultError)
		return nil, fmt.Errorf("re-quoting ticket params at %s wei face value to fund %s wei: %w",
			scaled, want, err)
	}
	s.metrics.IncTicketParamsFetch(metrics.ResultOK)

	if got := types.CreditedEV(retry.FaceValue, retry.WinProb); got.Cmp(want) < 0 {
		// Fail closed. Handing back a payment that funds less than asked
		// is how a caller ends up admitted and then refused for
		// insufficient balance.
		return nil, grpcstatus.Errorf(codes.FailedPrecondition,
			"payee will not quote parameters that fund %s wei: best offer credits %s wei "+
				"per ticket (face_value %s, win_prob %s)",
			want, got, retry.FaceValue, retry.WinProb)
	}
	// The rand must not move: a new one would be a different session and
	// a work_id the caller never agreed to.
	if len(current.RecipientRandHash) > 0 &&
		!bytes.Equal(current.RecipientRandHash, retry.RecipientRandHash) {
		return nil, grpcstatus.Errorf(codes.FailedPrecondition,
			"payee moved recipient rand while re-quoting face value; the session identity "+
				"is not the payer's to change")
	}
	return retry, nil
}

func (s *Service) findOrOpenSession(ctx context.Context, recipient []byte, faceValue *big.Int, capability, offering, ticketParamsBaseURL string, acceptedPrice *types.PriceInfo, acceptedQuote *pb.QuoteRef) (*senderSession, error) {
	key := sessionKey(recipient, capability, offering, ticketParamsBaseURL)

	s.mu.Lock()
	if sess, ok := s.sessions[key]; ok {
		sess.acceptedPrice = clonePriceInfo(acceptedPrice)
		sess.acceptedQuote = cloneQuoteRef(acceptedQuote)
		cached := cloneTicketParams(sess.ticketParams)
		s.mu.Unlock()
		// A cached session keeps the face value it was opened at, and a
		// LATER, larger funding request has to be funded from it. Sizing
		// the batch in tickets to compensate does not work: the payee
		// caps a session at store.MaxSenderNonces, so a small original
		// face value silently caps how much this session can ever fund —
		// 1,025 wei followed by 616,025 wei needed 601 tickets and the
		// payee rejected the last one, crediting 613,975 of 616,025.
		//
		// So the cached session is re-quoted at a larger face value
		// instead. The payee keeps its recipient rand for the tuple, so
		// work_id does not move and the session is the same one.
		if types.CreditedEV(cached.FaceValue, cached.WinProb).Cmp(faceValue) < 0 {
			rescaled, rerr := s.rescaleTicketParams(ctx, recipient, faceValue,
				capability, offering, ticketParamsBaseURL, cached)
			if rerr != nil {
				return nil, rerr
			}
			s.mu.Lock()
			// Re-read under the lock: another mint may have rescaled it
			// already, and the larger of the two is the one to keep.
			if live, still := s.sessions[key]; still {
				if types.CreditedEV(live.ticketParams.FaceValue, live.ticketParams.WinProb).Cmp(
					types.CreditedEV(rescaled.FaceValue, rescaled.WinProb)) < 0 {
					live.ticketParams = cloneTicketParams(rescaled)
				}
				sess = live
			}
			s.mu.Unlock()
		}
		return sess, nil
	}
	s.mu.Unlock()

	fetch := func(face *big.Int) (*types.TicketParams, error) {
		fetchStart := time.Now()
		got, ferr := s.fetcher.Fetch(ctx, TicketParamsRequest{
			BaseURL:    ticketParamsBaseURL,
			Sender:     append([]byte(nil), s.keystore.Address()...),
			Recipient:  append([]byte(nil), recipient...),
			FaceValue:  new(big.Int).Set(face),
			Capability: capability,
			Offering:   offering,
		})
		s.metrics.ObserveTicketParamsFetch(time.Since(fetchStart))
		if ferr != nil {
			s.metrics.IncTicketParamsFetch(metrics.ResultError)
			return nil, ferr
		}
		s.metrics.IncTicketParamsFetch(metrics.ResultOK)
		return got, nil
	}

	params, err := fetch(faceValue)
	if err != nil {
		return nil, err
	}

	// Open the session at a face value whose EXPECTED value carries the
	// funding intent, not one whose FACE value merely equals it.
	//
	// A ticket credits floor(face x win_prob / MaxWinProb) — roughly
	// face/1024 at the default probability — so opening at
	// face == funded left a caller with ~1/1024 of the credit it asked
	// for. The batch cannot simply grow to compensate: the payee caps a
	// session at store.MaxSenderNonces tickets, so a session's total
	// capacity is bounded and funding even the advertised minimum this
	// way is impossible.
	//
	// win_prob is the payee's to choose and is not known until it
	// answers, so the first fetch discovers it and the second asks for a
	// face value scaled to match. EV is linear in face value, so one
	// correction is enough. The tuple's recipient rand is stable, so
	// re-asking does not move work_id.
	if types.CreditedEV(params.FaceValue, params.WinProb).Cmp(faceValue) < 0 {
		params, err = s.rescaleTicketParams(ctx, recipient, faceValue,
			capability, offering, ticketParamsBaseURL, params)
		if err != nil {
			return nil, err
		}
	}
	workID := hex.EncodeToString(params.RecipientRandHash)
	sess := &senderSession{
		workID:        workID,
		cacheKey:      key,
		ticketParams:  cloneTicketParams(params),
		acceptedPrice: clonePriceInfo(acceptedPrice),
		acceptedQuote: cloneQuoteRef(acceptedQuote),
		capability:    capability,
		offering:      offering,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.sessions[key]; ok {
		s.workIDIndex[existing.workID] = key
		return existing, nil
	}
	s.sessions[key] = sess
	s.workIDIndex[sess.workID] = key
	s.metrics.SetSenderSessions(len(s.sessions))
	return sess, nil
}

func (s *Service) evictSessionByWorkID(workID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.workIDIndex[workID]
	if !ok {
		return false
	}
	delete(s.workIDIndex, workID)
	delete(s.sessions, key)
	s.metrics.SetSenderSessions(len(s.sessions))
	return true
}

type acceptedPriceInput struct {
	PricePerUnitWei int64
	UnitsPerPrice   uint64
	WorkUnitName    string
	QuoteRef        *pb.QuoteRef
	CapabilityName  string
	Offering        string
}

type fundingIntentInput struct {
	estimatedUnits uint64
	fundedValueWei *big.Int
}

func parseAcceptedPrice(in *pb.AcceptedPrice) (*acceptedPriceInput, error) {
	if in == nil {
		return nil, errors.New("accepted_price is required")
	}
	if strings.TrimSpace(in.GetCapability()) == "" {
		return nil, errors.New("capability is empty")
	}
	if strings.TrimSpace(in.GetOffering()) == "" {
		return nil, errors.New("offering is empty")
	}
	if strings.TrimSpace(in.GetWorkUnitName()) == "" {
		return nil, errors.New("work_unit_name is empty")
	}
	if in.GetUnitsPerPrice() == 0 {
		return nil, errors.New("units_per_price must be > 0")
	}
	pricePerUnitWei, err := parseBigUInt("price_per_unit_wei", in.GetPricePerUnitWei())
	if err != nil {
		return nil, err
	}
	if !pricePerUnitWei.IsInt64() {
		return nil, errors.New("price_per_unit_wei exceeds wire PriceInfo int64 range")
	}
	if in.GetQuoteRef() == nil {
		return nil, errors.New("quote_ref is required")
	}
	if strings.TrimSpace(in.GetQuoteRef().GetQuoteId()) == "" {
		return nil, errors.New("quote_ref.quote_id is empty")
	}
	if len(in.GetQuoteRef().GetConstraintFingerprint()) == 0 {
		return nil, errors.New("quote_ref.constraint_fingerprint is empty")
	}
	if len(in.GetQuoteRef().GetRouteFingerprint()) == 0 {
		return nil, errors.New("quote_ref.route_fingerprint is empty")
	}
	if in.GetUnitsPerPrice() > uint64(1<<63-1) {
		return nil, errors.New("units_per_price exceeds wire PriceInfo int64 range")
	}

	return &acceptedPriceInput{
		PricePerUnitWei: pricePerUnitWei.Int64(),
		UnitsPerPrice:   in.GetUnitsPerPrice(),
		WorkUnitName:    strings.TrimSpace(in.GetWorkUnitName()),
		QuoteRef:        cloneQuoteRef(in.GetQuoteRef()),
		CapabilityName:  strings.TrimSpace(in.GetCapability()),
		Offering:        strings.TrimSpace(in.GetOffering()),
	}, nil
}

func (a *acceptedPriceInput) toPriceInfo(estimatedUnits uint64) *types.PriceInfo {
	if a == nil {
		return nil
	}
	return &types.PriceInfo{
		PricePerUnit:  a.PricePerUnitWei,
		PixelsPerUnit: int64(a.UnitsPerPrice),
		Capability:    wireCapabilityID(a.CapabilityName),
		Constraint:    expectedPriceConstraint(a, estimatedUnits),
	}
}

func parseFundingIntent(in *pb.FundingIntent) (*fundingIntentInput, error) {
	if in == nil {
		return nil, errors.New("funding is required")
	}
	fundedValueWei, err := parseBigUInt("funded_value_wei", in.GetFundedValueWei())
	if err != nil {
		return nil, err
	}
	if fundedValueWei.Sign() <= 0 {
		return nil, errors.New("funded_value_wei must be > 0")
	}
	if in.GetMaxTotalUnits() > 0 && in.GetEstimatedUnits() > in.GetMaxTotalUnits() {
		return nil, errors.New("estimated_units exceeds max_total_units")
	}
	return &fundingIntentInput{
		estimatedUnits: in.GetEstimatedUnits(),
		fundedValueWei: fundedValueWei,
	}, nil
}

func parseBigUInt(field string, in *pb.BigUInt) (*big.Int, error) {
	if in == nil {
		return nil, fmt.Errorf("%s is required", field)
	}
	if len(in.GetValue()) == 0 {
		return nil, fmt.Errorf("%s is empty", field)
	}
	if len(in.GetValue()) > 32 {
		return nil, fmt.Errorf("%s exceeds uint256 size", field)
	}
	return new(big.Int).SetBytes(in.GetValue()), nil
}

func wireCapabilityID(capability string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(capability))
	return h.Sum32()
}

func expectedPriceConstraint(in *acceptedPriceInput, estimatedUnits uint64) string {
	ref := in.QuoteRef
	return fmt.Sprintf("cap=%s;off=%s;wu=%s;est=%d;qid=%s;qv=%d;cfp=%x;rfp=%x",
		url.QueryEscape(in.CapabilityName),
		url.QueryEscape(in.Offering),
		url.QueryEscape(in.WorkUnitName),
		estimatedUnits,
		url.QueryEscape(ref.GetQuoteId()),
		ref.GetQuoteVersion(),
		ref.GetConstraintFingerprint(),
		ref.GetRouteFingerprint(),
	)
}

func cloneQuoteRef(in *pb.QuoteRef) *pb.QuoteRef {
	if in == nil {
		return nil
	}
	return proto.Clone(in).(*pb.QuoteRef)
}

func clonePriceInfo(in *types.PriceInfo) *types.PriceInfo {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

// signOneTicket allocates the next nonce, builds a Ticket, hashes it,
// and signs with the keystore.
//
// The nonce comes from the STORE, not from the in-memory session. The
// receiver's replay ledger is durable, so a sender whose nonce was not
// would restart at 1 and replay nonces already consumed — every ticket
// rejected, nothing credited, and the broker serving work out of
// balance credited before the restart until it ran dry. Found on the
// pilot stack after a routine restart.
func (s *Service) signOneTicket(session *senderSession) (*types.TicketSenderParams, error) {
	nonce, err := s.nextNonce(session)
	if err != nil {
		return nil, err
	}

	params := session.ticketParams
	hash := ticketHash(&types.Ticket{
		Recipient:         params.Recipient,
		Sender:            s.keystore.Address(),
		FaceValue:         params.FaceValue,
		WinProb:           params.WinProb,
		SenderNonce:       nonce,
		RecipientRandHash: params.RecipientRandHash,
		CreationRound:     s.clock.LastInitializedRound(),
		CreationRoundHash: s.clock.LastInitializedL1BlockHash(),
	})
	sig, err := s.keystore.Sign(hash)
	if err != nil {
		return nil, err
	}
	s.metrics.IncTicketSigned()
	return &types.TicketSenderParams{SenderNonce: nonce, Sig: sig}, nil
}

// validateSenderInfo mirrors `pm.validateSenderInfo` from the prior
// reference impl: rejects when the sender has no deposit, no reserve,
// or an unlock is imminent.
func validateSenderInfo(info *providers.SenderInfo, currentRound int64) error {
	if info == nil {
		return errors.New("nil sender info")
	}
	if info.Deposit == nil || info.Deposit.Sign() == 0 {
		return errors.New("no sender deposit")
	}
	if info.Reserve == nil || info.Reserve.FundsRemaining == nil || info.Reserve.FundsRemaining.Sign() == 0 {
		return errors.New("no sender reserve")
	}
	if info.WithdrawRound != 0 && info.WithdrawRound <= currentRound+1 {
		return errors.New("deposit and reserve set to unlock soon")
	}
	return nil
}

// ticketHash returns the contract-defined keccak256 over the ticket's
// flatten layout (see types.Ticket.Hash). What gets EIP-191-wrapped and
// signed by the sender, and what `redeemWinningTicket` recomputes
// on-chain.
func ticketHash(t *types.Ticket) []byte {
	return t.Hash()
}

func evToBytes(ev *big.Rat) []byte {
	if ev == nil {
		return nil
	}
	num := ev.Num()
	den := ev.Denom()
	if den.Sign() == 0 {
		return nil
	}
	return new(big.Int).Quo(num, den).Bytes()
}

// sessionKey identifies a sender-side payment session.
//
// It deliberately does NOT include the funded value. Refill sizing must
// never change session identity: the payee pins its recipient rand — and
// therefore work_id — to the stable (sender, recipient, capability,
// offering) tuple for as long as its ticket session is open, so keying
// on funded value here only produced a second cache entry and a
// redundant ticket-params fetch that came back with the same identity.
// Worse, it implied the opposite invariant to anyone reading it.
//
// Face value is pinned at first issuance for the life of the session; a
// larger refill mints MORE tickets, not larger ones. See
// livepeer-network-protocol/protocols/offering-axes.md §6.2.
func sessionKey(recipient []byte, capability, offering string, ticketParamsBaseURL string) string {
	return hex.EncodeToString(recipient) + "|" + capability + "|" + offering + "|" + strings.TrimSpace(ticketParamsBaseURL)
}

func cloneTicketParams(in *types.TicketParams) *types.TicketParams {
	if in == nil {
		return nil
	}
	out := *in
	if in.FaceValue != nil {
		out.FaceValue = new(big.Int).Set(in.FaceValue)
	}
	if in.WinProb != nil {
		out.WinProb = new(big.Int).Set(in.WinProb)
	}
	if in.ExpirationBlock != nil {
		out.ExpirationBlock = new(big.Int).Set(in.ExpirationBlock)
	}
	if in.Recipient != nil {
		out.Recipient = append([]byte(nil), in.Recipient...)
	}
	if in.RecipientRandHash != nil {
		out.RecipientRandHash = append([]byte(nil), in.RecipientRandHash...)
	}
	if in.Seed != nil {
		out.Seed = append([]byte(nil), in.Seed...)
	}
	if in.ExpirationParams != nil {
		exp := *in.ExpirationParams
		if in.ExpirationParams.CreationRoundBlockHash != nil {
			exp.CreationRoundBlockHash = append([]byte(nil), in.ExpirationParams.CreationRoundBlockHash...)
		}
		out.ExpirationParams = &exp
	}
	return &out
}

// validityPeriodTTL bounds how stale the cached ticketValidityPeriod
// may be. Short, because a consumer's release decisions are derived from
// it and governance can change it at any time; not per-call, because a
// contract read on every mint puts the chain in the latency path of
// signing.
const validityPeriodTTL = 30 * time.Second

// ticketValidityPeriod returns the contract's value, refreshing when
// stale.
//
// It FAILS rather than falling back. A fallback here is not a
// conservative default in either direction: guess high and a consumer
// holds an encumbrance longer than needed, guess low and it releases
// while the envelope is still redeemable. Since the number exists to be
// published to somebody making money decisions with it, a daemon that
// cannot read it has nothing honest to say and must refuse to mint.
func (s *Service) ticketValidityPeriod(ctx context.Context) (int64, time.Time, error) {
	return s.readValidityPeriod(ctx, false)
}

// ticketValidityPeriodFresh bypasses the cache. Used by GetDepositInfo,
// which is an administrative query rather than a signing path: no
// latency argument for caching, so no reason to hand a consumer a value
// whose staleness they have to reason about.
func (s *Service) ticketValidityPeriodFresh(ctx context.Context) (int64, time.Time, error) {
	return s.readValidityPeriod(ctx, true)
}

func (s *Service) readValidityPeriod(ctx context.Context, fresh bool) (int64, time.Time, error) {
	s.validityMu.Lock()
	defer s.validityMu.Unlock()
	if !fresh && s.validityPeriod > 0 && time.Since(s.validityReadAt) < validityPeriodTTL {
		return s.validityPeriod, s.validityReadAt, nil
	}
	if s.broker == nil {
		return 0, time.Time{}, fmt.Errorf("no chain broker configured")
	}
	v, err := s.broker.TicketValidityPeriod(ctx)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("read ticketValidityPeriod: %w", err)
	}
	if s.validityPeriod > 0 && v != s.validityPeriod {
		s.logger.Warn("ticketValidityPeriod CHANGED on chain; envelopes already issued now "+
			"expire on a different round than they were told",
			"was", s.validityPeriod, "now", v)
	}
	s.validityPeriod = v
	s.validityReadAt = time.Now().UTC()
	return v, s.validityReadAt, nil
}

// nextNonce allocates durably when a store is configured, and falls back
// to the in-memory counter only when one is not — which is tests, and
// the in-process dev path that has no receiver to disagree with.
func (s *Service) nextNonce(session *senderSession) (uint32, error) {
	if s.store != nil && session.workID != "" {
		n, err := s.store.NextSenderNonce(session.workID)
		if err != nil {
			return 0, fmt.Errorf("allocate ticket nonce: %w", err)
		}
		// Keep the in-memory view in step so anything reading it for
		// diagnostics does not disagree with what was signed.
		s.mu.Lock()
		if n > session.nonce {
			session.nonce = n
		}
		s.mu.Unlock()
		return n, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session.nonce++
	return session.nonce, nil
}
