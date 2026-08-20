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

// Service implements pb.PayerDaemonServer.
type Service struct {
	pb.UnimplementedPayerDaemonServer

	keystore providers.KeyStore
	broker   providers.Broker
	clock    providers.Clock
	logger   *slog.Logger
	fetcher  TicketParamsFetcher
	metrics  metrics.Recorder

	// store is the durable mint-idempotency ledger. Minting signs
	// tickets against real deposit, so a retry after an uncertain
	// response must replay rather than re-sign, and that record has to
	// outlive the process.
	store *store.Store

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
func New(keystore providers.KeyStore, broker providers.Broker, clock providers.Clock, logger *slog.Logger, fetcher TicketParamsFetcher, rec metrics.Recorder, st *store.Store) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if rec == nil {
		rec = metrics.NewNoop()
	}
	return &Service{
		keystore:    keystore,
		broker:      broker,
		clock:       clock,
		logger:      logger,
		fetcher:     fetcher,
		metrics:     rec,
		store:       st,
		sessions:    map[string]*senderSession{},
		workIDIndex: map[string]string{},
	}
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
	prior, err := s.store.MintRecall(s.keystore.Address(), mintID, fingerprint)
	switch {
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

	tsp, err := s.signOneTicket(session)
	if err != nil {
		return nil, fmt.Errorf("sign ticket: %w", err)
	}

	batch := &types.TicketBatch{
		TicketParams: session.ticketParams,
		Sender:       s.keystore.Address(),
		ExpirationParams: &types.TicketExpirationParams{
			CreationRound:          s.clock.LastInitializedRound(),
			CreationRoundBlockHash: s.clock.LastInitializedL1BlockHash(),
		},
		TicketSenderParams: []*types.TicketSenderParams{tsp},
		ExpectedPrice:      session.acceptedPrice,
	}
	wire := batch.ToWirePayment()
	bytes, err := proto.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("marshal payment: %w", err)
	}

	ev := types.EV(session.ticketParams.FaceValue, session.ticketParams.WinProb)
	evBytes := evToBytes(ev)

	s.logger.Info("payment created",
		"work_id", session.workID,
		"capability", acceptedPrice.CapabilityName,
		"offering", acceptedPrice.Offering,
		"funded_value_wei", funding.fundedValueWei.String(),
		"ticket_face_value", session.ticketParams.FaceValue.String(),
		"nonce", tsp.SenderNonce)

	out := &pb.CreatePaymentResponse{
		PaymentBytes:     bytes,
		TicketsCreated:   1,
		ExpectedValue:    &pb.BigUInt{Value: evBytes},
		FundedValueWei:   &pb.BigUInt{Value: funding.fundedValueWei.Bytes()},
		AcceptedQuoteRef: cloneQuoteRef(session.acceptedQuote),
		WorkId:           session.workID,
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

// maxMintRequestIDBytes caps the caller-supplied key. Long enough for a
// prefixed UUID, short enough that the tombstone space stays predictable.
const maxMintRequestIDBytes = 128

// mintFingerprint binds a mint id to the request it paid for. A reused
// id with different content is refused rather than answered with the
// earlier payment, which would hand the caller a batch it never asked
// for.
func mintFingerprint(req *pb.CreatePaymentRequest) []byte {
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

func (s *Service) findOrOpenSession(ctx context.Context, recipient []byte, faceValue *big.Int, capability, offering, ticketParamsBaseURL string, acceptedPrice *types.PriceInfo, acceptedQuote *pb.QuoteRef) (*senderSession, error) {
	key := sessionKey(recipient, capability, offering, ticketParamsBaseURL)

	s.mu.Lock()
	if sess, ok := s.sessions[key]; ok {
		sess.acceptedPrice = clonePriceInfo(acceptedPrice)
		sess.acceptedQuote = cloneQuoteRef(acceptedQuote)
		s.mu.Unlock()
		return sess, nil
	}
	s.mu.Unlock()

	fetchStart := time.Now()
	params, err := s.fetcher.Fetch(ctx, TicketParamsRequest{
		BaseURL:    ticketParamsBaseURL,
		Sender:     append([]byte(nil), s.keystore.Address()...),
		Recipient:  append([]byte(nil), recipient...),
		FaceValue:  new(big.Int).Set(faceValue),
		Capability: capability,
		Offering:   offering,
	})
	s.metrics.ObserveTicketParamsFetch(time.Since(fetchStart))
	if err != nil {
		s.metrics.IncTicketParamsFetch(metrics.ResultError)
		return nil, err
	}
	s.metrics.IncTicketParamsFetch(metrics.ResultOK)
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

// signOneTicket increments the session's nonce, builds a Ticket, hashes
// it, and signs with the keystore.
func (s *Service) signOneTicket(session *senderSession) (*types.TicketSenderParams, error) {
	s.mu.Lock()
	session.nonce++
	nonce := session.nonce
	s.mu.Unlock()

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
