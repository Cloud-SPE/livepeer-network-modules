// Package settlement drives the on-chain redemption loop: pop the oldest
// pending winner, run gas pre-checks, submit the redemption via the
// Broker, mark redeemed on success / drain locally on terminal failure.
//
// The loop is single-threaded, one ticket per tick. The transaction
// itself — nonce, gas, replacement, confirmations, restart resume — is
// the Broker's concern, on chain-commons's durable intent machine (plan
// 0048 stage 4b); settlement only classifies what comes back.
package settlement

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	cerrors "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/errors"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/service/escrow"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/store"
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

// ChainValidityWindowRounds is how many rounds behind the current one a
// ticket's creation round may be and still be redeemable.
//
// This is the CHAIN's rule, not a local policy: the TicketBroker needs
// the creation round's block hash to verify a winning ticket, and that
// hash stops being available beyond the window, so redemption reverts.
// A daemon can configure a shorter window — it only stops trying sooner
// — but it cannot extend one.
//
// It is exported because it is also the answer to "when does an issued
// but never-admitted payment envelope become unspendable", which is the
// only unconditional release for an encumbrance held against one.
const ChainValidityWindowRounds = 2

const defaultValidityWindow = ChainValidityWindowRounds

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

	// Recorder receives redemption metrics. Nil = a no-op recorder.
	Recorder metrics.Recorder
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
	metrics  metrics.Recorder

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
	rec := cfg.Recorder
	if rec == nil {
		rec = metrics.NewNoop()
	}
	return &Settlement{
		store:    st,
		broker:   broker,
		gasPrice: gasPrice,
		clock:    clock,
		escrow:   esc,
		cfg:      cfg,
		log:      logger.With("component", "settlement"),
		metrics:  rec,
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
			s.metrics.SetCurrentRound(s.clock.LastInitializedRound())
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
	s.metrics.SetRedemptionQueueDepth(len(pend))
	if len(pend) == 0 {
		return nil, nil
	}
	p := pend[0]
	start := time.Now()
	err = s.attempt(ctx, p)
	s.metrics.ObserveRedemption(time.Since(start))
	s.metrics.IncRedemption(redemptionResultLabel(err))
	return p.Hash, err
}

// redemptionResultLabel maps an attempt() error to a bounded metric
// label.
func redemptionResultLabel(err error) string {
	switch {
	case err == nil:
		return metrics.RedeemRedeemed
	case errors.Is(err, ErrTicketExpired):
		return metrics.RedeemExpired
	case errors.Is(err, ErrTicketUsed):
		return metrics.RedeemAlreadyUsed
	case errors.Is(err, ErrFaceValueTooLow):
		return metrics.RedeemFaceValueTooLow
	case errors.Is(err, ErrInsufficientFunds):
		return metrics.RedeemInsufficientFund
	default:
		return metrics.RedeemTxError
	}
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
	s.metrics.SetGasPriceWei(metrics.WeiToFloat(gp))
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
	s.metrics.IncRedemptionTx(metrics.TxSubmitted)
	txHash, err := s.broker.RedeemWinningTicket(ctx, bt, t.Sig, t.RecipientRand)
	if err != nil {
		// The broker's own pre-check found the ticket already redeemed
		// (by an earlier attempt, or by the implementation this daemon
		// replaced). Nothing was sent; drain like the local pre-check.
		if errors.Is(err, providers.ErrTicketAlreadyUsed) {
			logCtx.Info("skip: broker reports ticket already redeemed on-chain")
			_ = s.drain(p.Hash, "used")
			return ErrTicketUsed
		}
		s.metrics.IncRedemptionTx(metrics.TxFailed)
		// Tx revert / contract refusal classified as "creationRound
		// does not have a block hash" maps to expired.
		if strings.Contains(err.Error(), "creationRound does not have a block hash") {
			logCtx.Info("redemption reverted as expired", "err", err)
			_ = s.drain(p.Hash, "expired")
			return ErrTicketExpired
		}
		// A revert is final for this ticket: the intent machine will not
		// send it again, so leaving it queued would only re-report the
		// same failure every tick. Drain it and surface the reason.
		if IsNonRetryable(err) {
			logCtx.Warn("redemption reverted; draining ticket", "err", err)
			_ = s.drain(p.Hash, "reverted")
			return fmt.Errorf("redeem: %w", err)
		}
		// Transient, not-found, circuit-open, cancelled tick: the ticket
		// stays queued and the same intent is waited on next tick.
		return fmt.Errorf("redeem: %w", err)
	}
	s.metrics.IncRedemptionTx(metrics.TxConfirmed)
	if err := s.store.MarkRedeemed(p.Hash, txHash, t, s.clock.LastInitializedRound()); err != nil {
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
	pend, err := s.store.PendingRedemptions()
	if err != nil {
		s.log.Warn("drain lookup failed", "ticket_hash", hex(ticketHash), "reason", reason, "err", err)
		return err
	}
	var ticket *store.SignedTicket
	for _, p := range pend {
		if bytesEqual(p.Hash, ticketHash) {
			ticket = p.Ticket
			break
		}
	}
	if ticket == nil {
		s.log.Warn("drain missing pending ticket", "ticket_hash", hex(ticketHash), "reason", reason)
		return fmt.Errorf("pending ticket not found")
	}
	if err := s.store.MarkRedeemed(ticketHash, zero, ticket, s.clock.LastInitializedRound()); err != nil {
		s.log.Warn("drain failed", "ticket_hash", hex(ticketHash), "reason", reason, "err", err)
		return err
	}
	return nil
}

// IsNonRetryable reports whether an error from RedeemNext is terminal —
// the ticket has been (or should be) drained from the queue and not
// retried.
//
// Terminal: the settlement sentinels (used, expired, face value too
// low), the broker's sentinels (already used, reverted), and anything
// chain-commons classifies as a revert. Everything else is worth
// another tick: transient transport failures, a not-yet-mined receipt,
// an open circuit, a cancelled tick, and even a "permanent" signing or
// wallet-funds failure, which an operator fixes without losing the
// ticket. ErrInsufficientFunds (the sender's escrow, not our wallet)
// is retryable for the same reason.
func IsNonRetryable(err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, ErrTicketUsed),
		errors.Is(err, ErrTicketExpired),
		errors.Is(err, ErrFaceValueTooLow),
		errors.Is(err, providers.ErrTicketAlreadyUsed),
		errors.Is(err, providers.ErrRedemptionReverted):
		return true
	}
	if strings.Contains(err.Error(), "creationRound does not have a block hash") {
		return true
	}
	// Classify returns the wrapped *cerrors.Error when there is one and
	// ClassTransient for anything it does not recognise, so a plain
	// error stays retryable.
	return cerrors.Classify(err).Class == cerrors.ClassReverted
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

func bytesEqual(a, b []byte) bool {
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
