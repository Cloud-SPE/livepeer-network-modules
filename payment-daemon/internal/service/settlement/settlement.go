// Package settlement drives the on-chain redemption loop: pop the oldest
// pending winner, run gas pre-checks, submit the redemption tx via the
// Broker, mark redeemed on success / drain locally on terminal failure.
//
// Per plan 0016 §11.Q1 we deliberately do NOT port the prior impl's
// chain-commons.txintent layer — settlement here is single-threaded,
// one tx per loop tick.
package settlement

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service/escrow"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/store"
)

// Sentinels.
var (
	// ErrTicketUsed: the on-chain TicketBroker reports usedTickets[hash]
	// already true. Drain locally; never retry.
	ErrTicketUsed = errors.New("settlement: ticket already used on-chain")

	// ErrTicketExpired: ticket's CreationRound is more than
	// ValidityWindow rounds behind LastInitializedRound. The contract
	// would revert with "creationRound does not have a block hash".
	// Drain locally; never retry.
	ErrTicketExpired = errors.New("settlement: ticket creationRound past validity window")

	// ErrFaceValueTooLow: faceValue ≤ redeemGas × gasPrice. Submitting
	// would lose money. Drain locally; never retry.
	ErrFaceValueTooLow = errors.New("settlement: face value below tx cost")

	// ErrInsufficientFunds: sender's available funds ≤ tx cost. Leave
	// queued; sender may top up.
	ErrInsufficientFunds = errors.New("settlement: insufficient sender funds")
)

const defaultValidityWindow = 2

// Config holds the settlement service's tunable state.
type Config struct {
	// RedeemGas is the gas limit used for redeemWinningTicket. Same
	// value as the broker's Config.RedeemGas; passed here so the gas-
	// cost preflight doesn't need a back-reference.
	RedeemGas uint64

	// ValidityWindow bounds how old (in rounds) a ticket's CreationRound
	// may be relative to Clock.LastInitializedRound. Zero = 2.
	ValidityWindow int64

	// Logger receives structured events. Nil = slog.Default().
	Logger *slog.Logger
}

// Settlement is the queue-and-redeem service.
type Settlement struct {
	store    *store.Store
	broker   providers.Broker
	gasPrice providers.GasPrice
	clock    providers.Clock
	escrow   *escrow.Escrow
	cfg      Config
	log      *slog.Logger

	stop chan struct{}
}

// New constructs a Settlement.
func New(st *store.Store, broker providers.Broker, gasPrice providers.GasPrice, clock providers.Clock, esc *escrow.Escrow, cfg Config) *Settlement {
	if cfg.RedeemGas == 0 {
		cfg.RedeemGas = 500_000
	}
	if cfg.ValidityWindow == 0 {
		cfg.ValidityWindow = defaultValidityWindow
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Settlement{
		store:    st,
		broker:   broker,
		gasPrice: gasPrice,
		clock:    clock,
		escrow:   esc,
		cfg:      cfg,
		log:      logger.With("component", "settlement"),
		stop:     make(chan struct{}),
	}
}

// Run drives the redemption loop until ctx is cancelled or Stop is
// called. Tick cadence is `interval`; one ticket per tick.
func (s *Settlement) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-t.C:
			rctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			_, _ = s.RedeemNext(rctx)
			cancel()
		}
	}
}

// Stop signals the redemption goroutine to exit.
func (s *Settlement) Stop() {
	select {
	case <-s.stop:
		return
	default:
		close(s.stop)
	}
}

// RedeemNext pops the oldest pending winner and attempts redemption.
// Returns (nil, nil) when the queue is empty. Otherwise (ticketHash,
// err) where err is nil on success.
func (s *Settlement) RedeemNext(ctx context.Context) ([]byte, error) {
	pend, err := s.store.PendingRedemptions()
	if err != nil {
		return nil, fmt.Errorf("list pending: %w", err)
	}
	if len(pend) == 0 {
		return nil, nil
	}
	p := pend[0]
	return p.Hash, s.attempt(ctx, p)
}

func (s *Settlement) attempt(ctx context.Context, p store.PendingRedemption) error {
	t := p.Ticket
	if t == nil {
		return s.drain(p.Hash, "nil_ticket")
	}
	logCtx := s.log.With(
		"ticket_hash", hex(p.Hash),
		"sender", hex(t.Sender),
		"face_value_wei", t.FaceValue.String(),
	)
	logCtx.Info("attempt redemption", "creation_round", t.CreationRound)

	if s.expired(t) {
		logCtx.Info("skip: ticket expired",
			"creation_round", t.CreationRound,
			"current_round", s.clock.LastInitializedRound(),
			"validity_window", s.cfg.ValidityWindow,
		)
		_ = s.drain(p.Hash, "expired")
		return ErrTicketExpired
	}

	used, err := s.broker.IsUsedTicket(ctx, p.Hash)
	if err != nil {
		return fmt.Errorf("isUsedTicket: %w", err)
	}
	if used {
		logCtx.Info("skip: ticket already redeemed on-chain")
		_ = s.drain(p.Hash, "used")
		return ErrTicketUsed
	}

	gp := s.gasPrice.Current()
	if gp == nil {
		gp = new(big.Int)
	}
	txCost := new(big.Int).Mul(big.NewInt(int64(s.cfg.RedeemGas)), gp)

	if t.FaceValue.Cmp(txCost) <= 0 {
		logCtx.Info("skip: face value below tx cost",
			"face_value_wei", t.FaceValue.String(),
			"tx_cost_wei", txCost.String(),
		)
		_ = s.drain(p.Hash, "face_value_too_low")
		return ErrFaceValueTooLow
	}

	avail, err := s.escrow.AvailableFunds(ctx, t.Sender)
	if err != nil {
		return fmt.Errorf("available funds: %w", err)
	}
	if avail.Cmp(txCost) <= 0 {
		logCtx.Info("skip: sender funds below tx cost",
			"available_wei", avail.String(),
			"tx_cost_wei", txCost.String(),
		)
		// Leave queued — sender may top up.
		return ErrInsufficientFunds
	}

	// Reserve face value as pending; release on exit regardless.
	s.escrow.SubFloat(t.Sender, t.FaceValue)
	defer func() {
		if err := s.escrow.AddFloat(t.Sender, t.FaceValue); err != nil {
			logCtx.Warn("escrow AddFloat after redemption", "err", err)
		}
	}()

	bt := &providers.Ticket{
		Recipient:         t.Recipient,
		Sender:            t.Sender,
		FaceValue:         new(big.Int).Set(t.FaceValue),
		WinProb:           new(big.Int).Set(t.WinProb),
		SenderNonce:       t.SenderNonce,
		RecipientRandHash: t.RecipientRandHash,
		CreationRound:     t.CreationRound,
		CreationRoundHash: t.CreationRoundHash,
	}
	txHash, err := s.broker.RedeemWinningTicket(ctx, bt, t.Sig, t.RecipientRand)
	if err != nil {
		// Tx revert / contract refusal classified as "creationRound
		// does not have a block hash" maps to expired.
		if strings.Contains(err.Error(), "creationRound does not have a block hash") {
			logCtx.Info("redemption reverted as expired", "err", err)
			_ = s.drain(p.Hash, "expired")
			return ErrTicketExpired
		}
		return fmt.Errorf("redeem: %w", err)
	}
	if err := s.store.MarkRedeemed(p.Hash, txHash); err != nil {
		return fmt.Errorf("mark redeemed: %w", err)
	}
	logCtx.Info("redemption confirmed", "tx_hash", hex(txHash))
	return nil
}

func (s *Settlement) expired(t *store.SignedTicket) bool {
	cur := s.clock.LastInitializedRound()
	if cur == 0 {
		return false
	}
	return cur-t.CreationRound > s.cfg.ValidityWindow
}

func (s *Settlement) drain(ticketHash []byte, reason string) error {
	zero := make([]byte, 32)
	if err := s.store.MarkRedeemed(ticketHash, zero); err != nil {
		s.log.Warn("drain failed", "ticket_hash", hex(ticketHash), "reason", reason, "err", err)
		return err
	}
	return nil
}

// IsNonRetryable reports whether an error from RedeemNext is terminal —
// the ticket has been (or should be) drained from the queue and not
// retried.
func IsNonRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrTicketUsed) {
		return true
	}
	if errors.Is(err, ErrTicketExpired) {
		return true
	}
	if errors.Is(err, ErrFaceValueTooLow) {
		return true
	}
	msg := err.Error()
	if strings.Contains(msg, "transaction failed") {
		return true
	}
	if strings.Contains(msg, "creationRound does not have a block hash") {
		return true
	}
	return false
}

// hex encodes bytes for log fields, with a leading 0x.
func hex(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 2+len(b)*2)
	out[0], out[1] = '0', 'x'
	for i, v := range b {
		out[2+2*i] = digits[v>>4]
		out[2+2*i+1] = digits[v&0x0f]
	}
	return string(out)
}
