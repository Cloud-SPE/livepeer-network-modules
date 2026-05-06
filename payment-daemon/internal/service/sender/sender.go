// Package sender implements the PayerDaemon RPC surface and the
// sender-side ticket-creation state machine.
//
// v0.2 scope:
//   - StartSession opens a session against a recipient/capability/offering
//     and returns the workID (hex of recipient_rand_hash).
//   - CreateTicketBatch signs `n` tickets monotonically against the
//     session, using a stubbed deterministic key (dev mode only).
//   - For v0.2 the receiver-issued TicketParams flow is stubbed — the
//     sender fabricates locally-deterministic params instead of calling
//     `PayeeDaemon.GetTicketParams`. Plan 0016 wires the real fetch.
//
// Future scope (plan 0016+):
//   - Real on-chain Broker queries via providers.Broker.
//   - Real ECDSA signing via providers.KeyStore.
//   - MaxEV / MaxTotalEV / DepositMultiplier validation per
//     `docs/operator-runbook.md` §Economics.
package sender

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
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

	mu       sync.Mutex
	sessions map[string]*senderSession // keyed by hex(recipientRandHash)
}

type senderSession struct {
	ticketParams *types.TicketParams
	nonce        uint32
	capability   string
	offering     string
}

// New constructs a sender Service.
func New(keystore providers.KeyStore, broker providers.Broker, clock providers.Clock, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		keystore: keystore,
		broker:   broker,
		clock:    clock,
		logger:   logger,
		sessions: map[string]*senderSession{},
	}
}

// CreatePayment implements pb.PayerDaemonServer.
//
// v0.2 fabricates stub TicketParams locally; plan 0016 fetches them
// from the receiver via PayeeDaemon.GetTicketParams over HTTP. The wire
// shape is identical so consumers don't change between v0.2 and 0016.
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

	// v0.2: defense-in-depth sender validation — query Broker for
	// deposit/reserve. Dev fake always returns "fine"; plan 0016's real
	// broker rejects on no-deposit / pending-unlock.
	info, err := s.broker.GetSenderInfo(ctx, s.keystore.Address())
	if err != nil {
		return nil, fmt.Errorf("get sender info: %w", err)
	}
	if err := validateSenderInfo(info, s.clock.LastInitializedRound()); err != nil {
		return nil, fmt.Errorf("sender validation: %w", err)
	}

	// Fabricate stub TicketParams. Plan 0016 swaps this for an HTTP
	// fetch against PayeeDaemon.GetTicketParams (proxied through the
	// worker's /v1/payment/ticket-params endpoint).
	params := stubTicketParams(req.GetRecipient(), faceValue, req.GetCapability(), req.GetOffering(), s.clock)

	// Find or open the session. The sender keys sessions by
	// recipient_rand_hash so consecutive CreatePayment calls against
	// the same recipient amortize the params.
	workID := hex.EncodeToString(params.RecipientRandHash)
	session := s.findOrOpenSession(workID, params, req.GetCapability(), req.GetOffering())

	// Sign one ticket. Plan 0015 will allow batches > 1 for interim
	// debits.
	tsp, err := s.signOneTicket(session, faceValue, params)
	if err != nil {
		return nil, fmt.Errorf("sign ticket: %w", err)
	}

	// Build the wire Payment.
	batch := &types.TicketBatch{
		TicketParams: params,
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

	ev := types.EV(faceValue, params.WinProb)
	evBytes := evToBytes(ev)

	s.logger.Info("payment created",
		"work_id", workID,
		"capability", req.GetCapability(),
		"offering", req.GetOffering(),
		"face_value", faceValue.String(),
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

func (s *Service) findOrOpenSession(workID string, params *types.TicketParams, capability, offering string) *senderSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[workID]; ok {
		return sess
	}
	sess := &senderSession{
		ticketParams: params,
		capability:   capability,
		offering:     offering,
	}
	s.sessions[workID] = sess
	return sess
}

// signOneTicket increments the session's nonce, builds a Ticket, hashes
// it, and signs with the keystore.
func (s *Service) signOneTicket(session *senderSession, faceValue *big.Int, params *types.TicketParams) (*types.TicketSenderParams, error) {
	s.mu.Lock()
	session.nonce++
	nonce := session.nonce
	s.mu.Unlock()

	hash := ticketHash(&types.Ticket{
		Recipient:         params.Recipient,
		Sender:            s.keystore.Address(),
		FaceValue:         faceValue,
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
//
// In dev mode the broker never returns these states; this is here so
// chain integration in plan 0016 is a provider swap, not an architecture
// change.
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

// stubTicketParams fabricates locally-deterministic TicketParams in lieu
// of an HTTP fetch from the receiver. Plan 0016 deletes this and wires
// the real fetch.
//
// Determinism: recipient_rand_hash = sha256(recipient || capability ||
// offering); seed is empty (the receiver derives the rand from the hash
// in real mode); win_prob is zero (so EV = 0) and ExpirationBlock is
// far in the future. Receiver in dev mode accepts any well-formed
// params without checking authorship.
func stubTicketParams(recipient []byte, faceValue *big.Int, capability, offering string, clock providers.Clock) *types.TicketParams {
	h := sha256.New()
	_, _ = h.Write(recipient)
	_, _ = h.Write([]byte(capability))
	_, _ = h.Write([]byte(offering))
	rand := h.Sum(nil)

	return &types.TicketParams{
		Recipient:         append([]byte(nil), recipient...),
		FaceValue:         new(big.Int).Set(faceValue),
		WinProb:           big.NewInt(0),
		RecipientRandHash: rand,
		Seed:              []byte{},
		ExpirationBlock:   new(big.Int).Add(clock.LastSeenL1Block(), big.NewInt(1_000_000)),
		ExpirationParams: &types.TicketExpirationParams{
			CreationRound:          clock.LastInitializedRound(),
			CreationRoundBlockHash: clock.LastInitializedL1BlockHash(),
		},
	}
}

// ticketHash is a stub hash. Plan 0016 replaces this with go-livepeer's
// `pm.Ticket.Hash()` (keccak256 over an EIP-712-like layout).
func ticketHash(t *types.Ticket) []byte {
	h := sha256.New()
	_, _ = h.Write(t.Recipient)
	_, _ = h.Write(t.Sender)
	if t.FaceValue != nil {
		_, _ = h.Write(t.FaceValue.Bytes())
	}
	if t.WinProb != nil {
		_, _ = h.Write(t.WinProb.Bytes())
	}
	var nonce [4]byte
	nonce[0] = byte(t.SenderNonce >> 24)
	nonce[1] = byte(t.SenderNonce >> 16)
	nonce[2] = byte(t.SenderNonce >> 8)
	nonce[3] = byte(t.SenderNonce)
	_, _ = h.Write(nonce[:])
	_, _ = h.Write(t.RecipientRandHash)
	_, _ = h.Write(t.CreationRoundHash)
	return h.Sum(nil)
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
