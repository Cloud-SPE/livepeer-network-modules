// Package sender implements the PayerDaemon RPC surface and the
// sender-side ticket-creation state machine.
//
// Current scope:
//   - CreatePayment fetches quote-free payee-issued TicketParams over
//     HTTP from the broker's `/v1/payment/ticket-params` endpoint.
//   - The sender caches sessions by (recipient, capability, offering,
//     requested target spend) so repeated calls reuse the same
//     recipient_rand_hash and nonce stream.
//   - Each payment is signed against the authoritative TicketParams
//     returned by the payee, not against a locally fabricated copy.
package sender

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"

	pb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/types"
	"google.golang.org/protobuf/proto"
)

// Service implements pb.PayerDaemonServer.
type Service struct {
	pb.UnimplementedPayerDaemonServer

	keystore providers.KeyStore
	broker   providers.Broker
	clock    providers.Clock
	logger   *slog.Logger
	fetcher  TicketParamsFetcher

	mu       sync.Mutex
	sessions map[string]*senderSession // keyed by recipient/capability/offering/target-spend tuple
}

type senderSession struct {
	workID       string
	cacheKey     string
	ticketParams *types.TicketParams
	nonce        uint32
	capability   string
	offering     string
}

// New constructs a sender Service.
func New(keystore providers.KeyStore, broker providers.Broker, clock providers.Clock, logger *slog.Logger, fetcher TicketParamsFetcher) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		keystore: keystore,
		broker:   broker,
		clock:    clock,
		logger:   logger,
		fetcher:  fetcher,
		sessions: map[string]*senderSession{},
	}
}

// CreatePayment implements pb.PayerDaemonServer.
func (s *Service) CreatePayment(ctx context.Context, req *pb.CreatePaymentRequest) (*pb.CreatePaymentResponse, error) {
	if len(req.GetRecipient()) == 0 {
		return nil, errors.New("recipient is empty")
	}
	if req.GetCapability() == "" {
		return nil, errors.New("capability is empty")
	}
	if req.GetOffering() == "" {
		return nil, errors.New("offering is empty")
	}
	faceValue, err := types.ParseFaceValue(req.GetFaceValue())
	if err != nil {
		return nil, fmt.Errorf("face_value: %w", err)
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
	if err := validateSenderInfo(info, s.clock.LastInitializedRound()); err != nil {
		return nil, fmt.Errorf("sender validation: %w", err)
	}

	session, err := s.findOrOpenSession(
		ctx,
		req.GetRecipient(),
		faceValue,
		req.GetCapability(),
		req.GetOffering(),
		req.GetTicketParamsBaseUrl(),
	)
	if err != nil {
		return nil, fmt.Errorf("ticket params: %w", err)
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
		ExpectedPrice:      &types.PriceInfo{}, // canonical zero on quote-free path
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
		"capability", req.GetCapability(),
		"offering", req.GetOffering(),
		"target_face_value", faceValue.String(),
		"ticket_face_value", session.ticketParams.FaceValue.String(),
		"nonce", tsp.SenderNonce)

	return &pb.CreatePaymentResponse{
		PaymentBytes:   bytes,
		TicketsCreated: 1,
		ExpectedValue:  evBytes,
	}, nil
}

// GetDepositInfo implements pb.PayerDaemonServer.
func (s *Service) GetDepositInfo(ctx context.Context, _ *pb.GetDepositInfoRequest) (*pb.GetDepositInfoResponse, error) {
	info, err := s.broker.GetSenderInfo(ctx, s.keystore.Address())
	if err != nil {
		return nil, err
	}
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

func (s *Service) findOrOpenSession(ctx context.Context, recipient []byte, faceValue *big.Int, capability, offering, ticketParamsBaseURL string) (*senderSession, error) {
	key := sessionKey(recipient, capability, offering, faceValue, ticketParamsBaseURL)

	s.mu.Lock()
	if sess, ok := s.sessions[key]; ok {
		s.mu.Unlock()
		return sess, nil
	}
	s.mu.Unlock()

	params, err := s.fetcher.Fetch(ctx, TicketParamsRequest{
		BaseURL:    ticketParamsBaseURL,
		Sender:     append([]byte(nil), s.keystore.Address()...),
		Recipient:  append([]byte(nil), recipient...),
		FaceValue:  new(big.Int).Set(faceValue),
		Capability: capability,
		Offering:   offering,
	})
	if err != nil {
		return nil, err
	}
	workID := hex.EncodeToString(params.RecipientRandHash)
	sess := &senderSession{
		workID:       workID,
		cacheKey:     key,
		ticketParams: cloneTicketParams(params),
		capability:   capability,
		offering:     offering,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.sessions[key]; ok {
		return existing, nil
	}
	s.sessions[key] = sess
	return sess, nil
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

func sessionKey(recipient []byte, capability, offering string, faceValue *big.Int, ticketParamsBaseURL string) string {
	target := ""
	if faceValue != nil {
		target = faceValue.String()
	}
	return hex.EncodeToString(recipient) + "|" + capability + "|" + offering + "|" + target + "|" + strings.TrimSpace(ticketParamsBaseURL)
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
